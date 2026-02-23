// Package main provides the API server entry point.
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
	defer func() {
		if err := app.Shutdown(context.Background()); err != nil {
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
		app.Logger.Info(ctx, "received shutdown signal, stopping server gracefully",
			slog.String("signal", ctx.Err().Error()),
		)
		return nil

	case err := <-errChan:
		app.Logger.Error(ctx, "server failed to start", err)
		return err
	}
}
