package workflow

import (
	"context"
	"log/slog"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel/codes"
)

// Runtime runs workflows. Only the workflow worker builds one: register every
// workflow and schedule on it, then call Launch.
type Runtime struct {
	ctx    dbos.Context
	pool   *pgxpool.Pool
	admin  *admin
	logger *logging.Logger
}

// NewRuntime connects DBOS to the system tables over pool, the process's own
// database pool, and verifies their version (the Atlas migrations create
// them). A database that is not reachable yet is tried again with a fresh
// DBOS context, because a failed one cannot be reused, until startupTimeout.
// It registers the default queue and the two admin schedules
// (report-workflows-needing-admin and delete-old-workflows).
func NewRuntime(ctx context.Context, pool *pgxpool.Pool, logger *logging.Logger) (*Runtime, error) {
	cfg := dbos.Config{
		AppName:            AppName,
		ExecutorID:         ExecutorID,
		ApplicationVersion: ApplicationVersion,
		SystemDBPool:       pool,
		DatabaseSchema:     Schema,
		SkipMigrations:     true,
		// Patch and DeprecatePatch refuse to run without it in v1.7.0
		// (dbos/workflow.go Patch). The fixed ApplicationVersion is kept: DBOS
		// only defaults the version when it is empty.
		EnablePatching: true,
		Logger:         logger.Slog(),
	}
	dctx, err := newContext(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}
	r := &Runtime{ctx: dctx, pool: pool, logger: logger}
	if _, err := dbos.RegisterQueue(dctx, DefaultQueue); err != nil {
		return nil, toAppErr(err, "register the default workflow queue")
	}
	if r.admin, err = newAdmin(logger); err != nil {
		return nil, err
	}
	if err := r.admin.register(r); err != nil {
		return nil, err
	}
	return r, nil
}

// newContext creates the DBOS context, trying again until startupTimeout.
func newContext(ctx context.Context, cfg dbos.Config, logger *logging.Logger) (dbos.Context, error) {
	deadline := time.Now().Add(startupTimeout)
	for {
		cfg.SystemDBStartupTimeout = time.Until(deadline)
		dctx, err := dbos.NewContext(ctx, cfg)
		if err == nil {
			return dctx, nil
		}
		if time.Until(deadline) < startupRetryInterval {
			return nil, toAppErr(err, "connect DBOS to its system tables")
		}
		logger.Warn(ctx, "DBOS could not reach its system tables; trying again",
			slog.String("error", err.Error()),
			slog.Duration("retry_in", startupRetryInterval),
		)
		select {
		case <-ctx.Done():
			return nil, toAppErr(ctx.Err(), "connect DBOS to its system tables")
		case <-time.After(startupRetryInterval):
		}
	}
}

// Launch starts the queue runners and the schedules and recovers the
// workflows the previous worker left. The worker is ready once it returns.
func (r *Runtime) Launch() error {
	return toAppErr(dbos.Launch(r.ctx), "launch the workflow runtime")
}

// Close stops running workflows, waiting up to 30 seconds, within the
// Deployment's termination grace period, and then closes the database pool
// NewRuntime was given: DBOS closes the system database pool even when it did
// not create it. Register it in a shutdown phase before the database's. A
// workflow interrupted here is recovered by the next Launch.
func (r *Runtime) Close() error {
	return toAppErr(dbos.Shutdown(r.ctx, 30*time.Second), "shut down the workflow runtime")
}

// Client returns the Client the worker uses for its own reports and for the
// `workflow` subcommands.
func (r *Runtime) Client() *Client {
	return &Client{c: r.ctx, pool: r.pool}
}

// registered adapts a usecase workflow method to DBOS. Each is registered as
// a configured instance named after the workflow: DBOS identifies a workflow
// function by its symbol, and every adapter shares one symbol per type pair,
// so the name is what tells them apart (dbos.WithInstance).
type registered[In, Out any] struct {
	name string
	fn   func(context.Context, usecase.Flow, In) (Out, error)
}

// ConfigName implements dbos.ConfiguredInstance.
func (w *registered[In, Out]) ConfigName() string { return w.name }

// run is the DBOS workflow function: it starts the run span and calls the
// usecase with a Flow of this run.
func (w *registered[In, Out]) run(ctx dbos.Context, in In) (Out, error) {
	status, err := ownStatus(ctx)
	if err != nil {
		return *new(Out), err
	}
	spanCtx, span := startRunSpan(ctx, status)
	defer span.End()
	out, err := w.fn(spanCtx, &flow{run: ctx}, in)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return out, err
}

// ownStatus reads the status of the running workflow: its trace attributes
// and how many times it has been run. The read is not a step, so a resumed
// run sees its current attempt count.
func ownStatus(ctx dbos.Context) (dbos.WorkflowStatus, error) {
	id, err := dbos.GetWorkflowID(ctx)
	if err != nil {
		return dbos.WorkflowStatus{}, toAppErr(err, "read the workflow id")
	}
	statuses, err := dbos.ListWorkflows(ctx,
		dbos.WithFilterWorkflowIDs(id),
		dbos.WithFilterLoadInput(false),
		dbos.WithFilterLoadOutput(false),
	)
	if err != nil {
		return dbos.WorkflowStatus{}, toAppErr(err, "read the workflow status")
	}
	if len(statuses) != 1 {
		return dbos.WorkflowStatus{ID: id}, nil
	}
	return statuses[0], nil
}

// Register registers fn as the workflow name. Call it before Launch, with the
// same names on every start: a stored workflow is resumed by its name.
func Register[In, Out any](r *Runtime, name string, fn func(context.Context, usecase.Flow, In) (Out, error)) {
	w := &registered[In, Out]{name: name, fn: fn}
	dbos.RegisterWorkflow(r.ctx, w.run, dbos.WithInstance(w), dbos.WithWorkflowName(name))
}
