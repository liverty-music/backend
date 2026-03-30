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
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// JobApp represents a lightweight application for batch jobs without an HTTP server.
// The CronJob searches for concerts and publishes events; concert persistence,
// notifications, and venue enrichment are handled by event consumers.
type JobApp struct {
	FollowRepo      entity.FollowRepository
	ConcertUC       usecase.ConcertUseCase
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeJobApp creates a JobApp with only the dependencies needed for batch processing.
func InitializeJobApp(ctx context.Context) (*JobApp, error) {
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
	artistRepo := rdb.NewArtistRepository(db)
	followRepo := rdb.NewFollowRepository(db)
	concertRepo := rdb.NewConcertRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	searchLogRepo := rdb.NewSearchLogRepository(db)

	// Infrastructure - Gemini
	var geminiSearcher entity.ConcertSearcher
	if cfg.GCP.ProjectID != "" {
		geminiHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
		searcher, err := gemini.NewConcertSearcher(ctx, gemini.Config{
			ProjectID:   cfg.GCP.ProjectID,
			Location:    cfg.GCP.Location,
			ModelName:   cfg.GCP.GeminiModel,
			DataStoreID: cfg.GCP.VertexAISearchDataStore,
		}, geminiHTTPClient, true, logger)
		if err != nil {
			return nil, err
		}
		geminiSearcher = searcher
	}

	// Infrastructure - Messaging Publisher
	if err := messaging.EnsureStreams(ctx, cfg.NATS); err != nil {
		return nil, fmt.Errorf("ensure NATS streams: %w", err)
	}

	wmLogger := watermill.NewSlogLogger(logger.Slog())
	var goChannel *gochannel.GoChannel
	if cfg.NATS.URL == "" {
		goChannel = gochannel.NewGoChannel(gochannel.Config{
			OutputChannelBuffer: 256,
		}, wmLogger)
	}
	publisher, err := messaging.NewPublisher(cfg.NATS, wmLogger, goChannel)
	if err != nil {
		return nil, fmt.Errorf("create messaging publisher: %w", err)
	}

	// Use Cases
	eventPublisher := messaging.NewEventPublisher(publisher)
	centroidResolver := geo.NewCentroidResolver()
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, searchLogRepo, geminiSearcher, centroidResolver, eventPublisher, infratelemetry.NewBusinessMetrics(), logger)

	// Register shutdown phases.
	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &JobApp{
		FollowRepo:      followRepo,
		ConcertUC:       concertUC,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
