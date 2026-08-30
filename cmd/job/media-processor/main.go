// Package main provides the media-processor consumer entry point.
//
// The media-processor subscribes to MEDIA.uploaded events, generates WebP
// variants (thumb + large) from the original uploaded by the organizer, writes
// them to the served CDN bucket, and cuts over series_media to the new media id
// in a single transaction so the old cover keeps serving until the new variants
// exist (no-404 window).
//
// Production binary must be built with libvips present:
//
//	CGO_ENABLED=1 go build -tags vips ./cmd/job/media-processor
//
// Without the vips tag the StubMediaProcessor is used, which terms every
// message immediately so the container crashloops — a deliberate signal that
// the build tag is missing.
package main

import (
	"context"
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
	if err := run(); err != nil {
		logger, _ := logging.New()
		logger.Error(context.Background(), "media-processor failed", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger, _ := logging.New()
	bootLogger.Info(ctx, "starting media-processor")

	var app *di.MediaJobApp
	defer func() {
		timeout := fallbackShutdownTimeout
		if app != nil {
			timeout = app.ShutdownTimeout
		}
		shutCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := shutdown.Shutdown(shutCtx); err != nil {
			bootLogger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	var err error
	app, err = di.InitializeMediaJobApp(ctx)
	if err != nil {
		return err
	}

	app.Logger.Info(ctx, "media-processor ready; waiting for events")

	if err := app.Router.Run(ctx); err != nil {
		return err
	}

	return nil
}
