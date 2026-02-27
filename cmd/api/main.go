// Package main provides the API server entry point.
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
		// Bootstrap logger for fatal error before exit.
		logger, _ := logging.New()
		logger.Error(context.Background(), "server failed", err)
		os.Exit(1)
	}
}

func run() error {
	// Create a context that will be canceled when OS signals are received
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt,    // SIGINT (Ctrl+C)
		syscall.SIGTERM, // SIGTERM (k8s termination signal)
		syscall.SIGQUIT, // SIGQUIT
	)
	defer stop()

	// Bootstrap logger for pre-initialization messages.
	bootLogger, _ := logging.New()
	bootLogger.Info(ctx, "starting server")

	app, err := di.InitializeApp(ctx)
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

	// Start server in a goroutine
	errChan := make(chan error, 1)

	go func() {
		if err := app.Server.Start(); err != nil {
			errChan <- err
		}
	}()

	// Wait for either context cancellation (signal) or server error
	select {
	case <-ctx.Done():
		// Go 1.26: context.Cause returns the specific OS signal that triggered cancellation.
		cause := context.Cause(ctx)
		app.Logger.Info(ctx, "received shutdown signal, stopping server gracefully",
			slog.String("cause", cause.Error()),
		)
		return nil

	case err := <-errChan:
		app.Logger.Error(ctx, "server failed to start", err)
		return err
	}
}
