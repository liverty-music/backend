package workflow_test

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitLimit bounds every wait on a workflow, so a broken runtime fails the
// test instead of hanging it.
const waitLimit = 60 * time.Second

// setupSystemDB gives the test a database of its own, a copy of the migrated
// template, with the DBOS system tables recreated empty, and returns its DSN.
// Nothing else uses the copy, so no other test, process or worktree recovers
// its pending workflows.
func setupSystemDB(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	dsn := testutil.NewDatabase(t).URL("app")

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `DROP SCHEMA IF EXISTS dbos CASCADE`)
	require.NoError(t, err)
	require.NoError(t, conn.Close(ctx))

	// DBOS creates its tables with the SQL TestDBOSMigrationsMatchModule keeps
	// equal to the Atlas migration, so the copy's own dbos schema is replaced
	// by the one DBOS builds. The copy already has the outbox the report
	// schedule reads (20261030130000_create_events_outbox.sql), empty.
	require.NoError(t, dbos.Migrate(ctx, dsn, dbos.WithMigrateSchema(workflow.Schema)))
	return dsn
}

// newPool returns the database pool of one process. DBOS closes the pool when
// the runtime or client using it shuts down, as in production, so every
// simulated process gets its own.
func newPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// newLogger returns a JSON logger writing into the returned buffer.
func newLogger(t *testing.T) (*logging.Logger, *syncBuffer) {
	t.Helper()
	buf := &syncBuffer{}
	logger, err := logging.New(logging.WithWriter(buf), logging.WithFormat(logging.FormatJSON))
	require.NoError(t, err)
	return logger, buf
}

// syncBuffer is a bytes.Buffer safe for the runtime's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startWorker builds a runtime, lets register add the workflows, launches it
// and closes it at the end of the test unless the test closed it first.
func startWorker(t *testing.T, dsn string, register func(*workflow.Runtime)) *workflow.Runtime {
	t.Helper()
	rt, _ := startWorkerWithLogs(t, dsn, register)
	return rt
}

// startWorkerWithLogs is startWorker that also returns what the worker logs.
func startWorkerWithLogs(t *testing.T, dsn string, register func(*workflow.Runtime)) (*workflow.Runtime, *syncBuffer) {
	t.Helper()
	pool := newPool(t, dsn)
	logger, logs := newLogger(t)
	rt, err := workflow.NewRuntime(context.Background(), pool, logger)
	require.NoError(t, err)
	register(rt)
	require.NoError(t, rt.Launch())
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { _ = rt.Close() }) })
	return rt, logs
}

// newClient returns a client as an API server builds it.
func newClient(t *testing.T, dsn string) *workflow.Client {
	t.Helper()
	pool := newPool(t, dsn)
	logger, _ := newLogger(t)
	c, err := workflow.NewClient(context.Background(), pool, logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// workflowStatus reads the stored status of the workflow id.
func workflowStatus(t *testing.T, dsn string, id string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { assert.NoError(t, conn.Close(ctx)) }()
	var status string
	require.NoError(t, conn.QueryRow(context.Background(), `SELECT status FROM dbos.workflow_status WHERE workflow_uuid = $1`, id).Scan(&status))
	return status
}

// waitFor fails the test unless ch receives within waitLimit.
func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(waitLimit):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// @spec components/infrastructure/backend/process/durable-workflows "Worker stops between two steps"
func TestRuntime_WorkerStopsBetweenTwoSteps(t *testing.T) {
	dsn := setupSystemDB(t)
	ctx := context.Background()

	var firstRuns, secondRuns atomic.Int32
	var stopping atomic.Bool
	stopping.Store(true)
	secondStarted := make(chan struct{}, 1)
	twoSteps := func(ctx context.Context, f usecase.Flow, in string) (string, error) {
		first, err := usecase.Step(ctx, f, "first", func(context.Context) (string, error) {
			firstRuns.Add(1)
			return in + "-first", nil
		})
		if err != nil {
			return "", err
		}
		return usecase.Step(ctx, f, "second", func(ctx context.Context) (string, error) {
			secondRuns.Add(1)
			if stopping.Load() {
				// The worker stops while this step runs: it never completes.
				secondStarted <- struct{}{}
				<-ctx.Done()
				return "", ctx.Err()
			}
			return first + "-second", nil
		})
	}
	register := func(rt *workflow.Runtime) { workflow.Register(rt, "two-steps", twoSteps) }

	old := startWorker(t, dsn, register)
	client := newClient(t, dsn)
	require.NoError(t, client.Start(ctx, "two-steps", "stop-between-steps", "in"))
	waitFor(t, secondStarted, "the second step to start")
	require.NoError(t, old.Close())
	assert.Equal(t, "PENDING", workflowStatus(t, dsn, "stop-between-steps"), "a stop is not a cancellation")

	stopping.Store(false)
	startWorker(t, dsn, register)
	var got string
	require.NoError(t, client.Await(ctx, "stop-between-steps", waitLimit, &got))

	assert.Equal(t, "in-first-second", got)
	assert.Equal(t, int32(1), firstRuns.Load(), "the completed first step does not run again")
	assert.Equal(t, int32(2), secondRuns.Load(), "only the interrupted second step runs again")
	assert.Equal(t, "SUCCESS", workflowStatus(t, dsn, "stop-between-steps"))
}

// @spec components/infrastructure/backend/process/durable-workflows "Same workflow started twice"
// @spec components/infrastructure/backend/process/event-delivery "Same request twice"
func TestRuntime_SameWorkflowStartedTwice(t *testing.T) {
	dsn := setupSystemDB(t)
	ctx := context.Background()

	var runs atomic.Int32
	once := func(ctx context.Context, f usecase.Flow, in string) (string, error) {
		return usecase.Step(ctx, f, "only", func(context.Context) (string, error) {
			runs.Add(1)
			return "result-" + in, nil
		})
	}
	startWorker(t, dsn, func(rt *workflow.Runtime) { workflow.Register(rt, "once", once) })
	servers := []*workflow.Client{newClient(t, dsn), newClient(t, dsn)}

	results := make([]string, len(servers))
	errs := make([]error, len(servers))
	var wg sync.WaitGroup
	for i, server := range servers {
		wg.Go(func() {
			if errs[i] = server.Start(ctx, "once", "started-twice", "in"); errs[i] != nil {
				return
			}
			errs[i] = server.Await(ctx, "started-twice", waitLimit, &results[i])
		})
	}
	wg.Wait()

	for i := range servers {
		require.NoError(t, errs[i])
		assert.Equal(t, "result-in", results[i])
	}
	assert.Equal(t, int32(1), runs.Load(), "the workflow runs once")
}

// needsAdmin reports whether a stored status is one the admin report counts.
func needsAdmin(status string) bool {
	return status == "ERROR" || status == "MAX_RECOVERY_ATTEMPTS_EXCEEDED" || status == "CANCELLED"
}

// The ten minutes are scaled down: the step fails with Unavailable on its
// first tries, spread over doubling intervals, and then succeeds.
//
// @spec components/infrastructure/backend/process/durable-workflows "Payment provider unavailable for 10 minutes"
func TestRuntime_ProviderUnavailableThenBack(t *testing.T) {
	defer workflow.ShortenStepRetries(10*time.Millisecond, 200*time.Millisecond)()
	dsn := setupSystemDB(t)
	ctx := context.Background()

	const failures = 6
	var tries atomic.Int32
	charge := func(ctx context.Context, f usecase.Flow, _ string) (string, error) {
		paid, err := usecase.Step(ctx, f, "charge", func(context.Context) (string, error) {
			if tries.Add(1) <= failures {
				return "", apperr.New(codes.Unavailable, "payment provider unavailable")
			}
			return "paid", nil
		})
		if err != nil {
			return "", err
		}
		return usecase.Step(ctx, f, "after", func(context.Context) (string, error) { return paid + "-then-issued", nil })
	}
	startWorker(t, dsn, func(rt *workflow.Runtime) { workflow.Register(rt, "charge", charge) })
	client := newClient(t, dsn)

	require.NoError(t, client.Start(ctx, "charge", "provider-back", ""))
	var got string
	require.NoError(t, client.Await(ctx, "provider-back", waitLimit, &got))

	assert.Equal(t, "paid-then-issued", got, "the workflow continues after the step")
	assert.Equal(t, int32(failures+1), tries.Load())
	assert.False(t, needsAdmin(workflowStatus(t, dsn, "provider-back")), "nothing is reported")
}

// The 24-hour deadline is scaled down to two seconds.
//
// @spec components/infrastructure/backend/process/durable-workflows "Deadline passed"
func TestRuntime_DeadlinePassed(t *testing.T) {
	defer workflow.ShortenStepRetries(10*time.Millisecond, 100*time.Millisecond)()
	dsn := setupSystemDB(t)
	ctx := context.Background()

	stuck := func(ctx context.Context, f usecase.Flow, _ string) (string, error) {
		return usecase.Step(ctx, f, "never-available", func(context.Context) (string, error) {
			return "", apperr.New(codes.Unavailable, "still unavailable")
		})
	}
	startWorker(t, dsn, func(rt *workflow.Runtime) { workflow.Register(rt, "stuck", stuck) })
	client := newClient(t, dsn)

	require.NoError(t, client.Start(ctx, "stuck", "deadline-passed", "", usecase.WithDeadline(time.Now().Add(2*time.Second))))
	var got string
	err := client.Await(ctx, "deadline-passed", waitLimit, &got)

	require.Error(t, err, "the workflow stops")
	assert.Empty(t, got, "it has no result")
	assert.True(t, needsAdmin(workflowStatus(t, dsn, "deadline-passed")), "it is reported for an admin, status %s", workflowStatus(t, dsn, "deadline-passed"))
}

// @spec components/infrastructure/backend/process/durable-workflows "API server restarts"
func TestRuntime_APIServerRestarts(t *testing.T) {
	dsn := setupSystemDB(t)
	ctx := context.Background()

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	slow := func(ctx context.Context, f usecase.Flow, _ string) (string, error) {
		return usecase.Step(ctx, f, "slow", func(context.Context) (string, error) {
			started <- struct{}{}
			<-release
			return "finished", nil
		})
	}
	startWorker(t, dsn, func(rt *workflow.Runtime) { workflow.Register(rt, "slow", slow) })

	// The API server that starts the workflow goes away while it runs.
	logger, _ := newLogger(t)
	server, err := workflow.NewClient(ctx, newPool(t, dsn), logger)
	require.NoError(t, err)
	require.NoError(t, server.Start(ctx, "slow", "outlives-its-starter", ""))
	waitFor(t, started, "the workflow to run")
	require.NoError(t, server.Close())
	close(release)

	var got string
	require.NoError(t, newClient(t, dsn).Await(ctx, "outlives-its-starter", waitLimit, &got))
	assert.Equal(t, "finished", got, "the workflow keeps running in the worker")
}
