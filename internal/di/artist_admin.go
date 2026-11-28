package di

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// InitializeArtistAdmin builds the artist use case the `artist` subcommands
// of the worker use: an admin runs them in the worker's Pod, so the writes go
// through the worker's own database identity. Only what
// ArtistUseCase.CorrectMBID needs is wired: the artist repository and the
// music catalog.
func InitializeArtistAdmin(ctx context.Context) (usecase.ArtistUseCase, *logging.Logger, error) {
	cfg, err := config.Load[config.WorkerConfig]()
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	// The command prints its result on stdout, so its logs go to stderr.
	logger, err := provideLogger(cfg.Logging, logging.WithWriter(os.Stderr))
	if err != nil {
		return nil, nil, err
	}
	shutdown.Init(logger)
	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, nil, err
	}
	artistRepo := rdb.NewArtistRepository(db, newOutbox(logger))

	extHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	musicbrainzClient := musicbrainz.NewClient(extHTTPClient, logger)

	artistUC := usecase.NewArtistUseCase(artistRepo, nil, nil, musicbrainzClient, nil, time.Now, logger)

	shutdown.AddExternalPhase(musicbrainzClient)
	shutdown.AddDatastorePhase(db)
	return artistUC, logger, nil
}
