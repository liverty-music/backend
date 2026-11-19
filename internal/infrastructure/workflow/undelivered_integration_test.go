package workflow_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/testutil"
)

// outboxPool returns a pool on a database of the test's own whose outbox is
// empty, so the report reads only the rows this test writes.
func outboxPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testutil.NewDatabase(t).URL("app"))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// @spec components/infrastructure/backend/process/event-delivery "Broker unreachable"
func TestAdmin_ReportsTheOldestUndeliveredAnnouncement(t *testing.T) {
	ctx := context.Background()
	pool := outboxPool(t)
	logger, logs := newLogger(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	age, err := workflow.ReportUndelivered(ctx, logger, pool, now)
	require.NoError(t, err)
	assert.Zero(t, age, "an empty outbox reports 0")

	_, err = pool.Exec(ctx, `INSERT INTO watermill_events_outbox (uuid, payload, create_time) VALUES ('a', '{"destination_topic":"FOLLOW.created"}', $1)`, now.Add(-11*time.Minute))
	require.NoError(t, err)

	age, err = workflow.ReportUndelivered(ctx, logger, pool, now)
	require.NoError(t, err)
	assert.Greater(t, age, 600.0)
	assert.Contains(t, logs.String(), workflow.LogKeyAnnouncementUndelivered)
	assert.Contains(t, logs.String(), "FOLLOW.created")
}
