// Package main provides the artist refresh CronJob entry point: once a day it
// reads one thirtieth of all artists again from MusicBrainz (name and whether
// their activity has ended).
package main

import (
	"context"
	"errors"
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

// errCircuitBroken fails the run when it stopped after consecutive failures,
// so the Job is recorded as failed.
var errCircuitBroken = errors.New("artist refresh stopped after consecutive failures")

func main() {
	logger := di.NewBootstrapLogger()
	if err := run(logger); err != nil {
		logger.Error(context.Background(), "artist refresh job failed", err)
		// Exit non-zero so the Job is recorded as failed. The CronJob's
		// backoffLimit: 0 keeps a systemic failure from being retried.
		os.Exit(1)
	}
}

func run(bootLogger *logging.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger.Info(ctx, "starting artist refresh job")

	// Register shutdown before DI so partially-initialized resources are
	// cleaned up even when initialization fails partway through.
	var app *di.ArtistRefreshJobApp
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
	app, err = di.InitializeArtistRefreshJobApp(ctx)
	if err != nil {
		return err
	}

	ctx, runSpan := telemetry.StartJobRun(ctx, "artist-refresh")
	defer runSpan.End()

	summary, err := app.ArtistUC.RefreshDue(ctx)
	if err != nil {
		return err
	}

	if cause := context.Cause(ctx); cause != nil {
		app.Logger.Info(ctx, "job interrupted by signal",
			slog.String("cause", cause.Error()),
		)
	}

	app.Logger.Info(ctx, "artist refresh job complete", analyticslog.Attrs(analyticslog.TopicJobResult,
		slog.Int("slot", summary.Slot),
		slog.Int("artists_listed", summary.Listed),
		slog.Int("artists_attempted", summary.Attempted),
		slog.Int("artists_succeeded", summary.Attempted-summary.Failed),
		slog.Int("artists_ended", summary.Ended),
		slog.Int("artists_renamed", summary.Renamed),
		slog.Int("failures", summary.Failed),
		slog.Bool("circuit_broken", summary.CircuitBroken),
	)...)

	if summary.CircuitBroken {
		return errCircuitBroken
	}
	return nil
}
