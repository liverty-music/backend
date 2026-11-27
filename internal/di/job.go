package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// JobApp represents a lightweight application for batch jobs without an HTTP server.
// The CronJob starts a search-concerts workflow for each followed artist that
// is due; the searches and the concert persistence run in the workflow
// worker, and notifications and venue enrichment in the event consumers.
type JobApp struct {
	FollowRepo      entity.FollowRepository
	ConcertUC       usecase.ConcertUseCase
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// provideJobConcertUseCase builds the ConcertUseCase of the discovery job:
// the repositories and the workflow client it starts each search through. The
// job calls no external search itself; the workflow worker does.
func provideJobConcertUseCase(ctx context.Context, cfg *config.JobConfig, db *rdb.Database, logger *logging.Logger) (usecase.ConcertUseCase, error) {
	// Repositories
	artistRepo := rdb.NewArtistRepository(db, newOutbox(logger))
	concertRepo := rdb.NewConcertRepository(db, newOutbox(logger))
	venueRepo := rdb.NewVenueRepository(db)
	seriesRepo := rdb.NewSeriesRepository(db, newOutbox(logger))
	organizerRepo := rdb.NewOrganizerRepository(db, newOutbox(logger))
	concertSearchRepo := rdb.NewConcertSearchRepository(db)
	concertCandidateRepo := rdb.NewConcertCandidateRepository(db)
	rejectedConcertRepo := rdb.NewRejectedConcertRepository(db)

	// Each search is started as a search-concerts workflow.
	workflows, err := provideWorkflows(ctx, db, logger)
	if err != nil {
		return nil, err
	}

	return usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, seriesRepo, organizerRepo, concertSearchRepo, concertCandidateRepo, rejectedConcertRepo, nil, rdb.NewAreaRepository(db), workflows, infratelemetry.NewBusinessMetrics(), nil, cfg.GCP.SearchCacheTTL(), cfg.GCP.SearchDiscoveryWindow(), time.Now, logger), nil
}

// InitializeJobApp creates a JobApp with only the dependencies needed for batch processing.
func InitializeJobApp(ctx context.Context) (*JobApp, error) {
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

	followRepo := rdb.NewFollowRepository(db, newOutbox(logger))
	concertUC, err := provideJobConcertUseCase(ctx, cfg, db, logger)
	if err != nil {
		return nil, err
	}

	// Register shutdown phases.
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &JobApp{
		FollowRepo:      followRepo,
		ConcertUC:       concertUC,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
