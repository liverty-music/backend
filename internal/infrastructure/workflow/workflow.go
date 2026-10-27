// Package workflow runs the backend's durable workflows on DBOS Transact, a
// library that keeps workflow state in the platform's own Postgres (schema
// `dbos`). The workflow worker (cmd/worker) is the only process that runs
// them: it builds a Runtime, registers every workflow and calls Launch. API
// servers, event consumers and jobs only start, signal and await workflows
// through a Client. Usecases see neither; they depend on usecase.Flow and
// usecase.Workflows (OpenSpec change introduce-durable-workflows).
package workflow

import (
	"context"
	"errors"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

const (
	// AppName is the DBOS application of the worker and of every client. Only
	// the owning application dequeues, runs and recovers a workflow, so it is
	// the same everywhere.
	AppName = "liverty-music-backend"

	// ExecutorID is fixed, so a new worker Pod recovers exactly the workflows
	// the old one left (single-server recovery; at most one worker runs at a
	// time).
	ExecutorID = "workflow-worker"

	// ApplicationVersion is fixed for the same reason: a worker recovers only
	// its own version. A change to a workflow's steps goes under a patch
	// (Flow.Patched); the version is never bumped for it.
	ApplicationVersion = "1"

	// Schema is the Postgres schema of the DBOS system tables. The Atlas
	// migrations create it; DBOS only verifies its version (SkipMigrations).
	Schema = "dbos"

	// DefaultQueue runs every workflow started without usecase.WithQueue.
	DefaultQueue = "workflows"

	// startupTimeout bounds the attempts to reach and verify the system
	// database at start, as startup-dependencies requires ("keep trying ...
	// for at least 5 minutes").
	startupTimeout = 5 * time.Minute

	// startupRetryInterval is the wait between two attempts.
	startupRetryInterval = 10 * time.Second
)

// Step defaults: a step that fails with Unavailable or DeadlineExceeded is
// tried again after 1 s, then at doubling intervals of at most 1 h. The limit
// is the workflow's deadline, not the count, because the in-memory count
// restarts when the worker does; the count is only high enough never to be
// reached first (design "Failure classification"). They are variables only so
// that tests can shorten the intervals (export_test.go).
var (
	stepBaseInterval  = time.Second
	stepBackoffFactor = 2.0
	stepMaxInterval   = time.Hour
	stepMaxRetries    = 10000
)

// stepOptions are the options of every step a usecase runs.
func stepOptions(name string) []dbos.StepOption {
	return []dbos.StepOption{
		dbos.WithStepName(name),
		dbos.WithStepRetryPredicate(isTransient),
		dbos.WithStepBaseInterval(stepBaseInterval),
		dbos.WithStepBackoffFactor(stepBackoffFactor),
		dbos.WithStepMaxInterval(stepMaxInterval),
		dbos.WithStepMaxRetries(stepMaxRetries),
	}
}

// isTransient reports whether a failed step is tried again.
func isTransient(err error) bool {
	return errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, apperr.ErrDeadlineExceeded)
}

// toAppErr gives an error that leaves this package an apperr code. An error
// that already carries one keeps it; DBOS's own errors stay reachable with
// errors.Is (dbos.ErrConflictingWorkflowID, dbos.ErrWorkflowCancelled), so a
// workflow that returns them lets DBOS handle them.
func toAppErr(err error, msg string) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*apperr.AppErr](err); ok {
		return err
	}
	code := codes.Internal
	switch {
	case errors.Is(err, dbos.ErrNonExistentWorkflow):
		code = codes.NotFound
	case errors.Is(err, dbos.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		code = codes.DeadlineExceeded
	case errors.Is(err, dbos.ErrWorkflowCancelled), errors.Is(err, dbos.ErrAwaitedWorkflowCancelled), errors.Is(err, context.Canceled):
		code = codes.Canceled
	case errors.Is(err, dbos.ErrConflictingWorkflowID):
		code = codes.Aborted
	}
	return apperr.Wrap(err, code, msg)
}
