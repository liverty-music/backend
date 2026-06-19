package di

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// SalesPhaseDiscoveryJobApp is the dependency bundle for the sales-phase
// discovery CronJob. The job enumerates upcoming series for all followed
// artists, calls the sales-phase searcher per series, upserts results, and
// publishes SALES_PHASE.discovered events for new phases.
type SalesPhaseDiscoveryJobApp struct {
	FollowRepo       entity.FollowRepository
	SalesPhaseDiscUC usecase.SalesPhaseDiscoveryUseCase
	Logger           *logging.Logger
	ShutdownTimeout  time.Duration
}

// InitializeSalesPhaseDiscoveryJobApp wires the sales-phase discovery job.
// A missing Gemini API key is a hard initialization error.
func InitializeSalesPhaseDiscoveryJobApp(ctx context.Context) (*SalesPhaseDiscoveryJobApp, error) {
	cfg, err := config.Load[config.JobConfig]()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger, err := provideLogger(cfg.Logging)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(logger.Slog())

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}

	// Repositories
	followRepo := rdb.NewFollowRepository(db)
	concertRepo := rdb.NewConcertRepository(db)
	artistRepo := rdb.NewArtistRepository(db)
	salesPhaseRepo := rdb.NewSalesPhaseRepository(db)

	// Messaging
	if err := messaging.EnsureStreams(ctx, cfg.NATS); err != nil {
		return nil, fmt.Errorf("ensure NATS streams: %w", err)
	}
	wmLogger := watermill.NewSlogLogger(logger.Slog())
	var goChannel *gochannel.GoChannel
	if cfg.NATS.URL == "" {
		goChannel = gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 256}, wmLogger)
	}
	publisher, err := messaging.NewPublisher(cfg.NATS, wmLogger, goChannel)
	if err != nil {
		return nil, fmt.Errorf("create messaging publisher: %w", err)
	}
	eventPublisher := messaging.NewEventPublisher(publisher)

	// Gemini sales-phase searcher — mandatory for this job.
	if cfg.GCP.GeminiSearchAPIKey == "" {
		return nil, fmt.Errorf("sales-phase-discovery job requires GCP_GEMINI_SEARCH_API_KEY")
	}
	geminiHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	searcher, err := gemini.NewSalesPhaseSearcher(ctx, gemini.SalesPhaseConfig{
		APIKey:          cfg.GCP.GeminiSearchAPIKey,
		ModelExtract:    cfg.GCP.SearchModelExtract(),
		ModelParse:      cfg.GCP.SearchModelParse(),
		Temperature:     cfg.GCP.GeminiSearchTemperature,
		ThinkingLevel:   cfg.GCP.GeminiSearchThinkingLevel,
		ThinkingExtract: cfg.GCP.GeminiSearchThinkingExtract,
		ThinkingParse:   cfg.GCP.GeminiSearchThinkingParse,
	}, geminiHTTPClient, logger)
	if err != nil {
		return nil, err
	}

	// Metrics wired for future observability; not yet plumbed into the UC.
	_ = infratelemetry.NewBusinessMetrics()

	salesPhaseDiscUC := usecase.NewSalesPhaseDiscoveryUseCase(
		concertRepo,
		artistRepo,
		salesPhaseRepo,
		searcher,
		eventPublisher,
		cfg.GCP.SalesPhaseWindow(),
		logger,
	)

	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &SalesPhaseDiscoveryJobApp{
		FollowRepo:       followRepo,
		SalesPhaseDiscUC: salesPhaseDiscUC,
		Logger:           logger,
		ShutdownTimeout:  cfg.ShutdownTimeout,
	}, nil
}
