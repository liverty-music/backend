package di

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/infrastructure/httpx"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// MerchDiscoveryJobApp is the dependency bundle for the merch-url discovery
// CronJob. The job lists in-window series, resolves each one's official merch
// URL via Gemini, and persists it fill-once; it needs no HTTP server, event
// publisher, or notification stack.
type MerchDiscoveryJobApp struct {
	MerchUC         usecase.MerchDiscoveryUseCase
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeMerchDiscoveryJobApp wires the merch-url discovery job. Unlike the
// concert-discovery job, the Gemini searcher is mandatory — resolving merch
// URLs is the job's entire purpose — so a missing API key is a hard
// initialization error rather than a degraded mode.
func InitializeMerchDiscoveryJobApp(ctx context.Context) (*MerchDiscoveryJobApp, error) {
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

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}

	seriesRepo := rdb.NewSeriesRepository(db)

	if cfg.GCP.GeminiSearchAPIKey == "" {
		return nil, fmt.Errorf("merch-discovery job requires GCP_GEMINI_SEARCH_API_KEY")
	}
	searcher, err := gemini.NewMerchSearcher(ctx, gemini.MerchConfig{
		APIKey:        cfg.GCP.GeminiSearchAPIKey,
		Model:         cfg.GCP.MerchModel(),
		Temperature:   cfg.GCP.GeminiSearchTemperature,
		ThinkingLevel: cfg.GCP.GeminiSearchThinkingLevel,
	}, &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}, logger)
	if err != nil {
		return nil, err
	}

	// Liveness probes hit arbitrary external hosts; the checker bounds each
	// request with its own context deadline, so the client needs no Timeout.
	checker := httpx.NewLivenessChecker(
		&http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
		logger,
	)

	merchUC := usecase.NewMerchDiscoveryUseCase(seriesRepo, searcher, checker, cfg.GCP.MerchWindow(), logger)

	shutdown.Init(logger)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &MerchDiscoveryJobApp{
		MerchUC:         merchUC,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
