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
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
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

// TicketSaleDiscoveryJobApp is the dependency bundle for the sales-phase
// discovery CronJob. The job enumerates upcoming series for all followed
// artists, calls the published-sale searcher once per artist, upserts results,
// and publishes SALES_PHASE.discovered events for new phases.
type TicketSaleDiscoveryJobApp struct {
	FollowRepo       entity.FollowRepository
	TicketSaleDiscUC usecase.TicketSaleDiscoveryUseCase
	Logger           *logging.Logger
	ShutdownTimeout  time.Duration
}

// InitializeTicketSaleDiscoveryJobApp wires the sales-phase discovery job.
// A missing Gemini API key is a hard initialization error.
func InitializeTicketSaleDiscoveryJobApp(ctx context.Context) (*TicketSaleDiscoveryJobApp, error) {
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
	followRepo := rdb.NewFollowRepository(db, newOutbox(logger))
	concertRepo := rdb.NewConcertRepository(db, newOutbox(logger))
	artistRepo := rdb.NewArtistRepository(db)
	ticketSaleRepo := rdb.NewTicketSaleRepository(db)
	ticketSaleSearchRepo := rdb.NewTicketSaleSearchRepository(db)
	journeyRepo := rdb.NewTicketJourneyRepository(db, newOutbox(logger))

	// Messaging
	//
	// Fail fast in non-local environments: a missing NATS_URL would silently
	// route published events to an in-process GoChannel that nothing consumes,
	// dropping every event. Local development still falls back to the
	// in-process GoChannel below.
	if !cfg.IsLocal() && cfg.NATS.URL == "" {
		return nil, fmt.Errorf("NATS_URL is required for the sales-phase-discovery job in non-local environments")
	}
	wmLogger := watermill.NewSlogLogger(logger.Slog())
	var goChannel *gochannel.GoChannel
	if cfg.NATS.URL == "" {
		goChannel = gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 256}, wmLogger)
	}
	publisher, err := messaging.NewPublisher(cfg.NATS, wmLogger, goChannel, logger)
	if err != nil {
		return nil, fmt.Errorf("create messaging publisher: %w", err)
	}
	eventPublisher := messaging.NewEventPublisher(publisher)

	// PostHog analytics client for the search signals (optional: nil without
	// POSTHOG_PROJECT_API_KEY).
	analyticsClient, err := provideAnalyticsClient(cfg.PostHog, cfg.Telemetry, logger)
	if err != nil {
		return nil, err
	}

	// Gemini published-sale searcher — mandatory for this job.
	if cfg.GCP.GeminiSearchAPIKey == "" {
		return nil, fmt.Errorf("sales-phase-discovery job requires GCP_GEMINI_SEARCH_API_KEY")
	}
	geminiHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	searcher, err := gemini.NewTicketSaleSearcher(ctx, gemini.TicketSaleSearcherConfig{
		APIKey:   cfg.GCP.GeminiSearchAPIKey,
		Model:    cfg.GCP.PublishedSaleSearchModel(),
		Thinking: cfg.GCP.PublishedSaleSearchThinking(),
		Reporter: posthog.NewGenerationReporter(analyticsClient, logger),
	}, geminiHTTPClient, logger)
	if err != nil {
		return nil, err
	}

	// Metrics wired for future observability; not yet plumbed into the UC.
	_ = infratelemetry.NewBusinessMetrics()

	ticketSaleDiscUC := usecase.NewTicketSaleDiscoveryUseCase(
		concertRepo,
		artistRepo,
		ticketSaleRepo,
		ticketSaleSearchRepo,
		journeyRepo,
		searcher,
		eventPublisher,
		analyticsClient,
		logger,
	)

	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &TicketSaleDiscoveryJobApp{
		FollowRepo:       followRepo,
		TicketSaleDiscUC: ticketSaleDiscUC,
		Logger:           logger,
		ShutdownTimeout:  cfg.ShutdownTimeout,
	}, nil
}
