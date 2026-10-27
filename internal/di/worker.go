package di

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// WorkerApp is the workflow worker: the only process that runs workflows,
// their queues and their schedules (OpenSpec change introduce-durable-workflows).
type WorkerApp struct {
	Runtime         *workflow.Runtime
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeWorkerApp connects to the database, waiting for it as every
// workload does, builds the workflow runtime and registers its workflows. The
// caller calls Runtime.Launch; the worker is ready once it returns.
//
// The worker registers only the runtime's own schedules so far. The first
// usecase workflow brings the usecases it needs into this graph.
func InitializeWorkerApp(ctx context.Context) (*WorkerApp, error) {
	cfg, err := config.Load[config.WorkerConfig]()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger, err := provideLogger(cfg.Logging)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(logger.Slog())
	shutdown.Init(logger)

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}
	shutdown.AddObservePhase(telemetryCloser)

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}

	runtime, err := workflow.NewRuntime(ctx, db.Pool.Raw(), logger)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	// Closers of one phase run at once, so the runtime stops its workflows in
	// the drain phase, while the database is still open. It also closes the
	// shared pool (DBOS closes the pool it is given); the database closes it
	// again, harmlessly, with the Cloud SQL dialer.
	shutdown.AddDrainPhase(runtime)
	shutdown.AddDatastorePhase(db)

	return &WorkerApp{
		Runtime:         runtime,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}

// InitializeWorkflowAdmin builds the workflow client the `workflow`
// subcommands use: an admin runs them in the worker's Pod, so the writes go
// through the worker's own database identity.
func InitializeWorkflowAdmin(ctx context.Context) (*workflow.Client, *logging.Logger, error) {
	cfg, err := config.Load[config.WorkerConfig]()
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	// The command prints its result on stdout, so its logs go to stderr.
	logger, err := provideLogger(cfg.Logging, logging.WithWriter(os.Stderr))
	if err != nil {
		return nil, nil, err
	}
	shutdown.Init(logger)
	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, nil, err
	}
	client, err := workflow.NewClient(ctx, db.Pool.Raw(), logger)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	shutdown.AddExternalPhase(client)
	shutdown.AddDatastorePhase(db)
	return client, logger, nil
}
