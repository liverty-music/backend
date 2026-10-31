package di

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// JobApp represents a lightweight application for batch jobs without an HTTP server.
// The CronJob searches for concerts and starts create-from-discovered for the
// new ones; concert persistence runs in the workflow worker, and notifications
// and venue enrichment in the event consumers.
type JobApp struct {
	FollowRepo      entity.FollowRepository
	ConcertUC       usecase.ConcertUseCase
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
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

	// Repositories
	artistRepo := rdb.NewArtistRepository(db, newOutbox(logger))
	followRepo := rdb.NewFollowRepository(db, newOutbox(logger))
	concertRepo := rdb.NewConcertRepository(db, newOutbox(logger))
	venueRepo := rdb.NewVenueRepository(db)
	seriesRepo := rdb.NewSeriesRepository(db, newOutbox(logger))
	organizerRepo := rdb.NewOrganizerRepository(db, newOutbox(logger))
	concertSearchRepo := rdb.NewConcertSearchRepository(db)
	concertCandidateRepo := rdb.NewConcertCandidateRepository(db)
	rejectedConcertRepo := rdb.NewRejectedConcertRepository(db)

	// The new concerts of a search are started as a create-from-discovered
	// workflow, which the workflow worker runs.
	workflows, err := provideWorkflows(ctx, db, logger)
	if err != nil {
		return nil, err
	}

	// Infrastructure - PostHog analytics client for the search signals
	// (optional: nil without POSTHOG_PROJECT_API_KEY).
	analyticsClient, err := provideAnalyticsClient(cfg.PostHog, cfg.Telemetry, logger)
	if err != nil {
		return nil, err
	}

	// Infrastructure - Gemini
	//
	// Fail fast in non-local environments: without the key the job would run
	// with no searcher and discover nothing. Local development may run without.
	if !cfg.IsLocal() && cfg.GCP.GeminiSearchAPIKey == "" {
		return nil, fmt.Errorf("GCP_GEMINI_SEARCH_API_KEY is required for the discovery job in non-local environments")
	}
	var geminiSearcher entity.ConcertSearcher
	if cfg.GCP.GeminiSearchAPIKey != "" {
		geminiHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
		searcher, err := gemini.NewConcertSearcher(ctx, gemini.Config{
			APIKey:          cfg.GCP.GeminiSearchAPIKey,
			Model:           cfg.GCP.ConcertSearchModel(),
			ThinkingLevel:   cfg.GCP.ConcertSearchThinkingLevel(),
			OmitTemperature: true,

			IncludeServerSideToolInvocations: true,
			Reporter:                         posthog.NewGenerationReporter(analyticsClient, logger),
		}, geminiHTTPClient, gemini.NewOfficialPageClient(), logger)
		if err != nil {
			return nil, err
		}
		geminiSearcher = searcher
	}

	// Use Cases
	centroidResolver := geo.NewCentroidResolver()
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, seriesRepo, organizerRepo, concertSearchRepo, concertCandidateRepo, rejectedConcertRepo, geminiSearcher, centroidResolver, workflows, infratelemetry.NewBusinessMetrics(), analyticsClient, cfg.GCP.SearchCacheTTL(), cfg.GCP.SearchDiscoveryWindow(), logger)

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
