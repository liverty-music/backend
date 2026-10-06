package di

import (
	"context"
	"net/http"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// OfficialSiteRefreshJobApp represents the official site refresh CronJob application.
type OfficialSiteRefreshJobApp struct {
	OfficialSiteRefreshUC usecase.OfficialSiteRefreshUseCase
	Logger                *logging.Logger
	ShutdownTimeout       time.Duration
}

// InitializeOfficialSiteRefreshJobApp creates an OfficialSiteRefreshJobApp with
// dependencies for refreshing artists' official sites from MusicBrainz.
func InitializeOfficialSiteRefreshJobApp(ctx context.Context) (*OfficialSiteRefreshJobApp, error) {
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
	followRepo := rdb.NewFollowRepository(db)

	// Infrastructure - MusicBrainz
	extHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	musicbrainzClient := musicbrainz.NewClient(extHTTPClient, logger)

	// Use Cases
	officialSiteRefreshUC := usecase.NewOfficialSiteRefreshUseCase(artistRepo, followRepo, musicbrainzClient, logger)

	// Register shutdown phases.
	shutdown.Init(logger)
	shutdown.AddExternalPhase(musicbrainzClient)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &OfficialSiteRefreshJobApp{
		OfficialSiteRefreshUC: officialSiteRefreshUC,
		Logger:                logger,
		ShutdownTimeout:       cfg.ShutdownTimeout,
	}, nil
}
