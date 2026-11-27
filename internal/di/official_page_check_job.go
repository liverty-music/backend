package di

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// OfficialPageCheckApp is the official-page-check mode of the concert
// discovery job: it reads the official pages of the followed artists the
// concert search would search and logs a digest of each, for the
// measurement of OpenSpec change measure-official-page-change-detection. It
// calls no Gemini API, starts no workflow and writes nothing to the database.
//
// TODO(measure-official-page-change-detection): remove with the mode after
// the measurement.
type OfficialPageCheckApp struct {
	FollowRepo      entity.FollowRepository
	ArtistRepo      entity.ArtistRepository
	OrganizerRepo   entity.OrganizerRepository
	PageClient      *http.Client
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeOfficialPageCheckApp creates an OfficialPageCheckApp from the
// concert discovery job's configuration.
func InitializeOfficialPageCheckApp(ctx context.Context) (*OfficialPageCheckApp, error) {
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
	shutdown.Init(logger)

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}

	// Register shutdown phases.
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &OfficialPageCheckApp{
		FollowRepo:      rdb.NewFollowRepository(db, newOutbox(logger)),
		ArtistRepo:      rdb.NewArtistRepository(db, newOutbox(logger)),
		OrganizerRepo:   rdb.NewOrganizerRepository(db, newOutbox(logger)),
		PageClient:      gemini.NewOfficialPageClient(),
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
