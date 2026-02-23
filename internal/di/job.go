package di

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	googleMaps "github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// JobApp represents a lightweight application for batch jobs without an HTTP server.
type JobApp struct {
	ArtistRepo         entity.ArtistRepository
	ConcertUC          usecase.ConcertUseCase
	VenueEnrichUC      usecase.VenueEnrichmentUseCase
	PushNotificationUC usecase.PushNotificationUseCase
	Logger             *logging.Logger
	closers            []io.Closer
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
	userRepo := provideUserRepository(db)
	searchLogRepo := rdb.NewSearchLogRepository(db)
	pushSubRepo := rdb.NewPushSubscriptionRepository(db)

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

	// Use Cases
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, userRepo, searchLogRepo, geminiSearcher, logger)
	pushNotificationUC := usecase.NewPushNotificationUseCase(
		artistRepo,
		pushSubRepo,
		logger,
		cfg.VAPID.PublicKey,
		cfg.VAPID.PrivateKey,
		cfg.VAPID.Contact,
	)

	// Infrastructure - Venue enrichment place searchers
	mbClient := musicbrainz.NewClient(nil, logger)
	mbSearcher := musicbrainz.NewPlaceSearcher(mbClient)

	var searchers []usecase.VenueNamedSearcher
	searchers = append(searchers, usecase.VenueNamedSearcher{Searcher: mbSearcher, AssignToMBID: true})
	if cfg.GoogleMapsAPIKey != "" {
		gmClient := googleMaps.NewClient(cfg.GoogleMapsAPIKey, nil, logger)
		gmSearcher := googleMaps.NewPlaceSearcher(gmClient)
		searchers = append(searchers, usecase.VenueNamedSearcher{Searcher: gmSearcher, AssignToMBID: false})
	}

	venueEnrichUC := usecase.NewVenueEnrichmentUseCase(venueRepo, venueRepo, logger, searchers...)

	return &JobApp{
		ArtistRepo:         artistRepo,
		ConcertUC:          concertUC,
		VenueEnrichUC:      venueEnrichUC,
		PushNotificationUC: pushNotificationUC,
		Logger:             logger,
		closers:            []io.Closer{db, telemetryCloser, mbClient},
	}, nil
}
