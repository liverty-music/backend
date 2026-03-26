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
	"time"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

// fallbackShutdownTimeout is used when DI initialization fails and
// app.ShutdownTimeout is unavailable. 10 seconds is generous for
// closing partially-initialized resources.
const fallbackShutdownTimeout = 10 * time.Second

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

	// Start the health probe server before DI so K8s can observe the pod
	// during initialization (healthz=200, readyz=503 until ready).
	healthSrv := server.NewHealthServer(":8081")
	go func() {
		if err := healthSrv.Start(); err != nil {
			bootLogger.Error(ctx, "health server failed", err)
		}
	}()
	// Ensure the health server is closed on all exit paths, including DI failure.
	// HealthServer.Close() is idempotent — the Drain phase may call it again safely.
	defer func() {
		if err := healthSrv.Close(); err != nil {
			bootLogger.Error(ctx, "health server close error", err)
		}
	}()

	// Register shutdown before DI so partially-initialized resources are
	// cleaned up even when initialization fails partway through.
	var app *di.ConsumerApp
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
	app, err = di.InitializeConsumerApp(ctx)
	if err != nil {
		return err
	}

	healthSrv.SetReady()
	shutdown.AddDrainPhase(healthSrv)

	app.Logger.Info(ctx, "consumer router starting")

	// Run the router in a goroutine so we can react to ctx cancellation.
	// Router.Run(ctx) internally closes the router when ctx is cancelled,
	// then blocks until all in-flight handlers complete (via closedCh).
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
		// Wait for Router.Run() to fully complete before proceeding to
		// shutdown phases. This ensures all in-flight message handlers
		// finish their DB writes and acks before publisher/DB are closed.
		if routerErr := <-errChan; routerErr != nil {
			app.Logger.Error(ctx, "router stopped with error during shutdown", routerErr)
		}
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
