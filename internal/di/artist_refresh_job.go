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

// ArtistRefreshJobApp represents the artist refresh CronJob application.
type ArtistRefreshJobApp struct {
	ArtistUC        usecase.ArtistUseCase
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeArtistRefreshJobApp creates an ArtistRefreshJobApp with the
// dependencies for reading artists again from MusicBrainz.
func InitializeArtistRefreshJobApp(ctx context.Context) (*ArtistRefreshJobApp, error) {
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
	artistRepo := rdb.NewArtistRepository(db, newOutbox(logger))

	// Infrastructure - MusicBrainz
	extHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	musicbrainzClient := musicbrainz.NewClient(extHTTPClient, logger)

	// Use Cases. The job calls only ArtistUseCase.RefreshDue, which needs the
	// repository, the catalog and the clock; the chart lookups and their
	// cache belong to the API server.
	artistUC := usecase.NewArtistUseCase(artistRepo, nil, nil, musicbrainzClient, nil, time.Now, logger)

	// Register shutdown phases.
	shutdown.Init(logger)
	shutdown.AddExternalPhase(musicbrainzClient)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &ArtistRefreshJobApp{
		ArtistUC:        artistUC,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
