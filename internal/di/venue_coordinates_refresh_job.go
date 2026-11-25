package di

import (
	"context"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// VenueCoordinatesRefreshJobApp represents the venue coordinates refresh
// CronJob application.
type VenueCoordinatesRefreshJobApp struct {
	VenueUC         usecase.VenueUseCase
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeVenueCoordinatesRefreshJobApp creates a
// VenueCoordinatesRefreshJobApp with the Venues and the map catalog the
// coordinates are read from again.
func InitializeVenueCoordinatesRefreshJobApp(ctx context.Context) (*VenueCoordinatesRefreshJobApp, error) {
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

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}

	venueRepo := rdb.NewVenueRepository(db)
	areaRepo := rdb.NewAreaRepository(db)
	mapCatalog, err := newVenueMapCatalog(ctx, cfg.GCP.ProjectID, cfg.IsLocal(), areaRepo, logger)
	if err != nil {
		return nil, err
	}

	venueUC := usecase.NewVenueUseCase(mapCatalog, venueRepo, areaRepo)

	shutdown.Init(logger)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &VenueCoordinatesRefreshJobApp{
		VenueUC:         venueUC,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
