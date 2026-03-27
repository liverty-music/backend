package di

import (
	"context"
	"net/http"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/music/fanarttv"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ImageSyncJobApp represents the artist image sync CronJob application.
type ImageSyncJobApp struct {
	ArtistRepo      entity.ArtistRepository
	ImageSyncUC     usecase.ArtistImageSyncUseCase
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeImageSyncJobApp creates an ImageSyncJobApp with dependencies for image syncing.
func InitializeImageSyncJobApp(ctx context.Context) (*ImageSyncJobApp, error) {
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

	// Repositories
	artistRepo := rdb.NewArtistRepository(db)

	// Infrastructure - fanart.tv
	extHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	fanarttvClient := fanarttv.NewClient(cfg.FanartTVAPIKey, extHTTPClient, logger)
	logoFetcher := fanarttv.NewLogoFetcher(extHTTPClient)

	// Use Cases
	imageSyncUC := usecase.NewArtistImageSyncUseCase(artistRepo, fanarttvClient, logoFetcher, logger)

	// Register shutdown phases.
	shutdown.Init(logger)
	shutdown.AddExternalPhase(fanarttvClient)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &ImageSyncJobApp{
		ArtistRepo:      artistRepo,
		ImageSyncUC:     imageSyncUC,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
