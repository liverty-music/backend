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
	"github.com/liverty-music/backend/pkg/shutdown"
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
	// Use a fresh context with a deadline aligned to the K8s termination budget,
	// so that phases are skipped if shutdown runs too long.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), app.ShutdownTimeout)
		defer cancel()
		if err := shutdown.Shutdown(ctx); err != nil {
			app.Logger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	// Start the health probe server in the background for K8s readiness/liveness.
	go func() {
		if err := app.HealthServer.Start(); err != nil {
			app.Logger.Error(ctx, "health server failed", err)
		}
	}()

	app.Logger.Info(ctx, "consumer router starting")

	// Run the router in a goroutine so we can react to ctx cancellation.
	// Router.Run(ctx) internally closes the router when ctx is cancelled.
	errChan := make(chan error, 1)
	go func() {
		if err := app.Router.Run(ctx); err != nil {
			errChan <- err
		}
		close(errChan)
	}()

	select {
	case <-ctx.Done():
		// Go 1.26: context.Cause returns the specific OS signal.
		cause := context.Cause(ctx)
		app.Logger.Info(ctx, "received shutdown signal, stopping consumer gracefully",
			slog.String("cause", cause.Error()),
		)
		return nil

	case err := <-errChan:
		if err != nil {
			app.Logger.Error(ctx, "consumer router stopped with error", err,
				slog.Any("signal", ctx.Err()),
			)
			return err
		}
		app.Logger.Info(ctx, "consumer router stopped gracefully")
		return nil
	}
}
