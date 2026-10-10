// Package main provides the official site refresh CronJob entry point.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

// fallbackShutdownTimeout is used when DI initialization fails and
// app.ShutdownTimeout is unavailable.
const fallbackShutdownTimeout = 10 * time.Second

func main() {
	logger := di.NewBootstrapLogger()
	if err := run(logger); err != nil {
		logger.Error(context.Background(), "official site refresh job failed", err)
		// Exit non-zero so the Job is recorded as failed. The CronJob's
		// backoffLimit: 0 keeps a systemic failure from being retried.
		os.Exit(1)
	}
}

func run(bootLogger *logging.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger.Info(ctx, "starting official site refresh job")

	// Register shutdown before DI so partially-initialized resources are
	// cleaned up even when initialization fails partway through.
	var app *di.OfficialSiteRefreshJobApp
	defer func() {
		timeout := fallbackShutdownTimeout
		if app != nil {
			timeout = app.ShutdownTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := shutdown.Shutdown(ctx); err != nil {
			bootLogger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	var err error
	app, err = di.InitializeOfficialSiteRefreshJobApp(ctx)
	if err != nil {
		return err
	}

	summary, err := app.OfficialSiteRefreshUC.RefreshDueOfficialSites(ctx)
	if err != nil {
		return err
	}

	if cause := context.Cause(ctx); cause != nil {
		app.Logger.Info(ctx, "job interrupted by signal",
			slog.String("cause", cause.Error()),
		)
	}

	app.Logger.Info(ctx, "official site refresh job complete",
		slog.Int("followed_artists", summary.Followed),
		slog.Int("batch_size", summary.BatchSize),
		slog.Int("artists_due", summary.Due),
		slog.Int("artists_attempted", summary.Attempted),
		slog.Int("artists_succeeded", summary.Attempted-summary.Failed),
		slog.Int("failures", summary.Failed),
		slog.Bool("circuit_broken", summary.CircuitBroken),
	)

	return nil
}
