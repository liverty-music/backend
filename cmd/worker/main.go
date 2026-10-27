// Package main is the workflow worker: the only backend process that runs
// durable workflows, their queues and their schedules (OpenSpec change
// introduce-durable-workflows). Run with the arguments `workflow ...`, it is
// instead the admin command that lists, resumes, forks and deletes workflows
// (see workflow_command.go).
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

// healthAddr serves /healthz and /readyz for the Deployment's probes.
const healthAddr = ":8081"

// fallbackShutdownTimeout is used when DI fails before the configured
// timeout is known.
const fallbackShutdownTimeout = 10 * time.Second

func main() {
	if len(os.Args) > 1 && os.Args[1] == "workflow" {
		os.Exit(runWorkflowCommand(os.Args[2:], os.Stdout, os.Stderr))
	}

	logger := di.NewBootstrapLogger()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()
	if err := run(ctx, logger, server.NewHealthServer(healthAddr), di.InitializeWorkerApp); err != nil {
		logger.Error(context.Background(), "workflow worker failed", err)
		os.Exit(1)
	}
}

// run starts the health server, builds the worker and launches the workflow
// runtime, then runs until ctx ends. /healthz answers from the start and does
// not depend on the database, so a database that is down at start never gets
// the Pod restarted; /readyz succeeds only once the runtime runs its queues
// and schedules.
func run(ctx context.Context, bootLogger *logging.Logger, health *server.HealthServer, initialize func(context.Context) (*di.WorkerApp, error)) error {
	go func() {
		if err := health.Start(); err != nil {
			bootLogger.Error(ctx, "health server failed", err)
		}
	}()
	defer func() {
		if err := health.Close(); err != nil {
			bootLogger.Error(context.Background(), "health server close error", err)
		}
	}()

	var app *di.WorkerApp
	defer func() {
		timeout := fallbackShutdownTimeout
		if app != nil {
			timeout = app.ShutdownTimeout
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := shutdown.Shutdown(shutdownCtx); err != nil {
			bootLogger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	var err error
	app, err = initialize(ctx)
	if err != nil {
		return err
	}
	if err := app.Runtime.Launch(); err != nil {
		return err
	}
	health.SetReady()
	app.Logger.Info(ctx, "workflow worker running")

	<-ctx.Done()
	health.SetShuttingDown()
	app.Logger.Info(ctx, "received shutdown signal, stopping workflow worker",
		slog.String("cause", context.Cause(ctx).Error()),
	)
	return nil
}
