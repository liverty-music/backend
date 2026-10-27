package workflow_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// useManualMeterReader makes the global meter provider one the test reads.
// Tests that call it do not run in parallel.
func useManualMeterReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	return reader
}

// needsAdminGauge returns the workflow.needs_admin value for the workflow
// name and status, and whether the series exists.
func needsAdminGauge(t *testing.T, reader *sdkmetric.ManualReader, name, status string) (int64, bool) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	want := attribute.NewSet(attribute.String("workflow_name", name), attribute.String("status", status))
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "workflow.needs_admin" {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			require.True(t, ok, "workflow.needs_admin is an int64 gauge")
			for _, dp := range gauge.DataPoints {
				if dp.Attributes.Equals(&want) {
					return dp.Value, true
				}
			}
		}
	}
	return 0, false
}

// runSchedule runs the named schedule now and waits for the run to end.
func runSchedule(t *testing.T, client *workflow.Client, name string) {
	t.Helper()
	ctx := context.Background()
	id, err := workflow.TriggerSchedule(client, name)
	require.NoError(t, err)
	var n int
	require.NoError(t, client.Await(ctx, id, waitLimit, &n))
}

// needsAdminLogLines counts the report's log lines for the workflow id.
func needsAdminLogLines(t *testing.T, logs *syncBuffer, id string) int {
	t.Helper()
	n := 0
	for line := range strings.SplitSeq(logs.String(), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "log line %q", line)
		if entry["msg"] == workflow.LogKeyWorkflowNeedsAdmin && entry["workflow_id"] == id {
			n++
		}
	}
	return n
}

// fails is a workflow whose only step fails with an error nobody planned for.
func fails(ctx context.Context, f usecase.Flow, _ string) (string, error) {
	return usecase.Step(ctx, f, "unexpected", func(context.Context) (string, error) {
		return "", apperr.New(codes.Internal, "unexpected")
	})
}

// @spec components/infrastructure/backend/process/durable-workflows "Unexpected error"
func TestAdmin_UnexpectedErrorIsReported(t *testing.T) {
	reader := useManualMeterReader(t)
	dsn := setupSystemDB(t)
	ctx := context.Background()
	_, logs := startWorkerWithLogs(t, dsn, func(rt *workflow.Runtime) { workflow.Register(rt, "fails", fails) })
	client := newClient(t, dsn)

	// Before anything fails, every registered workflow reads 0, so the gauge
	// shows the report runs and the alert has data.
	runSchedule(t, client, workflow.ReportScheduleName)
	for _, name := range []string{"fails", workflow.ReportScheduleName, workflow.RetentionScheduleName} {
		for _, status := range []string{"ERROR", "MAX_RECOVERY_ATTEMPTS_EXCEEDED", "CANCELLED"} {
			value, ok := needsAdminGauge(t, reader, name, status)
			require.True(t, ok, "a series for %s %s", name, status)
			assert.Equal(t, int64(0), value, "%s %s", name, status)
		}
	}

	require.NoError(t, client.Start(ctx, "fails", "unexpected-error", ""))
	var got string
	err := client.Await(ctx, "unexpected-error", waitLimit, &got)
	require.Error(t, err, "the workflow ends with the step's error")
	assert.Equal(t, "ERROR", workflowStatus(t, dsn, "unexpected-error"))

	// The schedule runs every 5 minutes; the test runs the next tick now.
	runSchedule(t, client, workflow.ReportScheduleName)
	value, ok := needsAdminGauge(t, reader, "fails", "ERROR")
	require.True(t, ok, "the gauge has a series for the workflow's name")
	assert.Equal(t, int64(1), value)
	assert.Equal(t, 1, needsAdminLogLines(t, logs, "unexpected-error"))

	runSchedule(t, client, workflow.ReportScheduleName)
	assert.Equal(t, 1, needsAdminLogLines(t, logs, "unexpected-error"), "a workflow is logged once, not on every run")

	require.NoError(t, client.Delete(ctx, "unexpected-error"))
	runSchedule(t, client, workflow.ReportScheduleName)
	value, ok = needsAdminGauge(t, reader, "fails", "ERROR")
	require.True(t, ok)
	assert.Equal(t, int64(0), value, "the gauge returns to 0 once an admin resolved the workflow")
}

// workflowExists reports whether the workflow id still has a record.
func workflowExists(t *testing.T, dsn, id string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { assert.NoError(t, conn.Close(ctx)) }()
	var exists bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM dbos.workflow_status WHERE workflow_uuid = $1)`, id).Scan(&exists))
	return exists
}

// @spec components/infrastructure/backend/process/durable-workflows "Old finished workflow"
// @spec components/infrastructure/backend/process/durable-workflows "Old failed workflow"
// @spec components/infrastructure/backend/process/durable-workflows "Workflow still waiting"
func TestAdmin_DeleteOldWorkflows(t *testing.T) {
	reader := useManualMeterReader(t)
	dsn := setupSystemDB(t)
	ctx := context.Background()

	finishes := func(ctx context.Context, f usecase.Flow, _ string) (string, error) {
		return usecase.Step(ctx, f, "done", func(context.Context) (string, error) { return "done", nil })
	}
	waits := func(ctx context.Context, f usecase.Flow, _ string) (string, error) {
		_, _, err := f.Recv(ctx, "never-sent", 24*time.Hour)
		return "", err
	}
	rt := startWorker(t, dsn, func(rt *workflow.Runtime) {
		workflow.Register(rt, "finishes", finishes)
		workflow.Register(rt, "fails", fails)
		workflow.Register(rt, "waits", waits)
	})
	client := newClient(t, dsn)

	require.NoError(t, client.Start(ctx, "finishes", "old-finished", ""))
	require.NoError(t, client.Start(ctx, "fails", "old-failed", ""))
	require.NoError(t, client.Start(ctx, "waits", "still-waiting", ""))
	var got string
	require.NoError(t, client.Await(ctx, "old-finished", waitLimit, &got))
	require.Error(t, client.Await(ctx, "old-failed", waitLimit, &got))

	// The clock moves forward instead of the test waiting: 29 days keeps
	// everything, 40 days is past the 30 for all three, and only the state
	// decides what is deleted.
	t.Run("nothing is deleted before 30 days", func(t *testing.T) {
		workflow.SetClock(rt, func() time.Time { return time.Now().Add(29 * 24 * time.Hour) })
		runSchedule(t, client, workflow.RetentionScheduleName)
		assert.True(t, workflowExists(t, dsn, "old-finished"))
	})

	workflow.SetClock(rt, func() time.Time { return time.Now().Add(40 * 24 * time.Hour) })
	runSchedule(t, client, workflow.RetentionScheduleName)

	assert.False(t, workflowExists(t, dsn, "old-finished"), "an old finished workflow is deleted")
	assert.True(t, workflowExists(t, dsn, "old-failed"), "an old failed workflow is kept")
	assert.True(t, workflowExists(t, dsn, "still-waiting"), "a waiting workflow is kept")
	assert.Equal(t, "PENDING", workflowStatus(t, dsn, "still-waiting"))

	runSchedule(t, client, workflow.ReportScheduleName)
	value, ok := needsAdminGauge(t, reader, "fails", "ERROR")
	require.True(t, ok)
	assert.Equal(t, int64(1), value, "the old failed workflow is still reported for an admin")
}
