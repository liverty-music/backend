package di

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// JobApp represents a lightweight application for batch jobs without an HTTP server.
type JobApp struct {
	ArtistRepo entity.ArtistRepository
	ConcertUC  usecase.ConcertUseCase
	Logger     *logging.Logger
	closers    []io.Closer
}

// Shutdown closes all resources held by the job application.
func (a *JobApp) Shutdown(_ context.Context) error {
	log.Println("Starting job shutdown...")

	var errs error
	for _, closer := range a.closers {
		if err := closer.Close(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to close resource: %w", err))
		}
	}

	if errs != nil {
		return errs
	}

	log.Println("Job shutdown complete")
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

	// Use Case
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, searchLogRepo, geminiSearcher, logger)

	return &JobApp{
		ArtistRepo: artistRepo,
		ConcertUC:  concertUC,
		Logger:     logger,
		closers:    []io.Closer{db, telemetryCloser},
	}, nil
}
