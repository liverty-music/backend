package di

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// JobApp represents a lightweight application for batch jobs without an HTTP server.
// The CronJob searches for concerts and publishes events; concert persistence,
// notifications, and venue enrichment are handled by event consumers.
type JobApp struct {
	ArtistRepo entity.ArtistRepository
	ConcertUC  usecase.ConcertUseCase
	Logger     *logging.Logger
	closers    []io.Closer
}

// Shutdown closes all resources held by the job application.
func (a *JobApp) Shutdown(ctx context.Context) error {
	a.Logger.Info(ctx, "starting job shutdown")

	var errs error
	for _, closer := range a.closers {
		if err := closer.Close(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to close resource: %w", err))
		}
	}

	if errs != nil {
		return errs
	}

	a.Logger.Info(ctx, "job shutdown complete")
	return nil
}

// InitializeJobApp creates a JobApp with only the dependencies needed for batch processing.
func InitializeJobApp(ctx context.Context) (*JobApp, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger, err := provideLogger(cfg)
	if err != nil {
		return nil, err
	}

	db, err := rdb.New(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Repositories
	artistRepo := rdb.NewArtistRepository(db)
	concertRepo := rdb.NewConcertRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	userRepo := rdb.NewUserRepository(db)
	searchLogRepo := rdb.NewSearchLogRepository(db)

	// Infrastructure - Gemini
	var geminiSearcher entity.ConcertSearcher
	if cfg.GCP.ProjectID != "" {
		searcher, err := gemini.NewConcertSearcher(ctx, gemini.Config{
			ProjectID:   cfg.GCP.ProjectID,
			Location:    cfg.GCP.Location,
			ModelName:   cfg.GCP.GeminiModel,
			DataStoreID: cfg.GCP.VertexAISearchDataStore,
		}, nil, logger)
		if err != nil {
			return nil, err
		}
		geminiSearcher = searcher
	}

	// Infrastructure - Messaging Publisher
	wmLogger := watermill.NewStdLogger(false, false)
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
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, userRepo, searchLogRepo, geminiSearcher, publisher, logger)

	return &JobApp{
		ArtistRepo: artistRepo,
		ConcertUC:  concertUC,
		Logger:     logger,
		closers:    []io.Closer{db, telemetryCloser, publisher},
	}, nil
}
