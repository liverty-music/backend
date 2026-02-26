// Package main provides the event consumer entry point.
// It runs a Watermill Router that subscribes to NATS JetStream (or GoChannel
// in local development) and processes events from the concert discovery pipeline.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/liverty-music/backend/internal/di"
	"github.com/pannpers/go-logging/logging"
)

func main() {
	if err := run(); err != nil {
		logger, _ := logging.New()
		logger.Error(context.Background(), "consumer failed", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGQUIT,
	)
	defer stop()

	bootLogger, _ := logging.New()
	bootLogger.Info(ctx, "starting event consumer")

	app, err := di.InitializeConsumerApp(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Shutdown(ctx); err != nil {
			app.Logger.Error(ctx, "error during shutdown", err)
		}
	}()

	app.Logger.Info(ctx, "consumer router starting")

	// Router.Run blocks until ctx is cancelled or a fatal error occurs.
	if err := app.Router.Run(ctx); err != nil {
		app.Logger.Error(ctx, "consumer router stopped with error", err,
			slog.String("signal", ctx.Err().Error()),
		)
		return err
	}

	app.Logger.Info(ctx, "consumer router stopped gracefully")
	return nil
}
