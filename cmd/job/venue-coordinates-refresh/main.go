// Package main provides the venue coordinates refresh CronJob entry point.
// It reads again the Venues' coordinates that are missing or older than 20
// days from the map catalog and removes those older than 30 days.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// fallbackShutdownTimeout is used when DI initialization fails and
// app.ShutdownTimeout is unavailable.
const fallbackShutdownTimeout = 10 * time.Second

func main() {
	logger := di.NewBootstrapLogger()
	if err := run(logger); err != nil {
		logger.Error(context.Background(), "venue coordinates refresh job failed", err)
		// Exit non-zero so the Job is recorded as failed; the next day's run
		// lists the Venues this one could not refresh.
		os.Exit(1)
	}
}

func run(bootLogger *logging.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	start := time.Now()
	bootLogger.Info(ctx, "starting venue coordinates refresh job")

	var app *di.VenueCoordinatesRefreshJobApp
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
	app, err = di.InitializeVenueCoordinatesRefreshJobApp(ctx)
	if err != nil {
		return err
	}

	ctx, runSpan := telemetry.StartJobRun(ctx, "venue-coordinates-refresh")
	defer runSpan.End()

	summary, err := app.VenueUC.RefreshCoordinates(ctx, start)
	if summary != nil {
		app.Logger.Info(ctx, "venue coordinates refresh job complete", analyticslog.Attrs(analyticslog.TopicJobResult,
			slog.Int("venues_due", summary.Due),
			slog.Int("venues_refreshed", summary.Refreshed),
			slog.Int("venues_removed", summary.Removed),
			slog.Int("failures", summary.Failed),
			slog.Int64("venues_expired", summary.Expired),
		)...)
	}
	return err
}
