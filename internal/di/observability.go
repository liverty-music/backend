package di

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/liverty-music/backend/internal/infrastructure/server"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

// startGoroutineLeakDetection wires the Go 1.27 goroutine-leak surfaces when
// enabled: an internal-only pprof listener (for on-demand full profiles) and a
// periodic sampler that publishes the backend_goroutine_leak_count gauge (for
// alerting). Both are no-ops when disabled, so local/dev runs and short-lived
// jobs skip the overhead. Shared by the api and consumer bootstrappers.
//
// The debug listener is registered with the shutdown Drain phase; the sampler
// stops when ctx is canceled.
func startGoroutineLeakDetection(ctx context.Context, cfg config.GoroutineLeakConfig, workload string, logger *logging.Logger) {
	if !cfg.Enabled {
		logger.Info(ctx, "goroutine leak detection disabled")
		return
	}

	debugSrv := server.NewDebugServer(cfg, logger)
	go func() {
		if err := debugSrv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error(ctx, "debug (pprof) server failed", err)
		}
	}()
	shutdown.AddDrainPhase(debugSrv)

	infratelemetry.NewGoroutineLeakMonitor(workload, cfg.SampleInterval, logger).Start(ctx)

	logger.Info(ctx, "goroutine leak detection enabled",
		slog.Int("debug_port", cfg.Port),
		slog.Duration("sample_interval", cfg.SampleInterval),
	)
}
