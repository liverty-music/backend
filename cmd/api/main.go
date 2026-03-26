// Package main provides the API server entry point.
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

	// Register shutdown before DI so partially-initialized resources are
	// cleaned up even when initialization fails partway through.
	var app *di.App
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
	app, err = di.InitializeApp(ctx)
	if err != nil {
		return err
	}

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
