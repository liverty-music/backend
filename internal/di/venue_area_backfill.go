package di

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
)

// VenueAreaBackfillApp is the one-off `backfill-venue-areas` subcommand of
// the concert-discovery job image.
type VenueAreaBackfillApp struct {
	Backfill        *usecase.VenueAreaBackfill
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeVenueAreaBackfillApp creates the backfill with the job's own
// database and Gemini credentials. It needs the Gemini key.
func InitializeVenueAreaBackfillApp(ctx context.Context) (*VenueAreaBackfillApp, error) {
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
	shutdown.Init(logger)

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}
	shutdown.AddDatastorePhase(db)

	httpClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport), Timeout: 2 * time.Minute}
	asker, err := gemini.NewVenueAreaAsker(ctx, cfg.GCP.GeminiSearchAPIKey, cfg.GCP.ConcertSearchModel(), httpClient, logger)
	if err != nil {
		return nil, fmt.Errorf("create the venue area asker: %w", err)
	}

	return &VenueAreaBackfillApp{
		Backfill:        usecase.NewVenueAreaBackfill(rdb.NewVenueRepository(db), rdb.NewAreaRepository(db), asker, logger),
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
