package rdb_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock advances instantly on every wait and records the waits.
type fakeClock struct {
	now   time.Time
	waits []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

func newJSONLogger(t *testing.T) (*logging.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger, err := logging.New(logging.WithFormat(logging.FormatJSON), logging.WithWriter(buf))
	require.NoError(t, err)
	return logger, buf
}

func levels(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var out []string
	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		var e map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &e))
		out = append(out, e["level"].(string))
	}
	return out
}

// @spec components/infrastructure/backend/process/startup-dependencies "Database unreachable beyond the wait"
func TestWaitForDatabase_GivesUpAfterBudget(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger(t)
	clk := &fakeClock{now: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)}
	start := clk.now
	var attempts []time.Time
	unreachable := errors.New("dial tcp: connection refused")

	err := rdb.WaitForDatabase(context.Background(), func(context.Context) error {
		attempts = append(attempts, clk.now)
		return unreachable
	}, logger, clk)

	require.ErrorIs(t, err, unreachable)
	assert.Contains(t, err.Error(), "database unreachable for 5m0s")
	assert.GreaterOrEqual(t, attempts[len(attempts)-1].Sub(start), rdb.StartupBudget,
		"the attempts span at least the budget")
	assert.Equal(t, []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 15 * time.Second},
		clk.waits[:6], "backoff doubles from 1 s up to 15 s")
	for _, s := range levels(t, buf) {
		assert.Equal(t, "WARN", s, "retries are warnings; main logs the final failure")
	}
}

func TestWaitForDatabase_SucceedsAfterFailures(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger(t)
	clk := &fakeClock{}
	calls := 0

	err := rdb.WaitForDatabase(context.Background(), func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("connection refused")
		}
		return nil
	}, logger, clk)

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
	assert.Equal(t, []string{"WARN", "WARN", "INFO"}, levels(t, buf))
}

func TestWaitForDatabase_StopsWhenContextEnds(t *testing.T) {
	t.Parallel()
	logger, _ := newJSONLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rdb.WaitForDatabase(ctx, func(context.Context) error {
		return errors.New("connection refused")
	}, logger, blockingClock{})

	require.ErrorIs(t, err, context.Canceled)
}

// blockingClock never fires, so only ctx can end the wait.
type blockingClock struct{}

func (blockingClock) Now() time.Time                       { return time.Time{} }
func (blockingClock) After(time.Duration) <-chan time.Time { return nil }
