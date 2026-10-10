package rpc_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/grpchealth"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPinger is a database whose ping result the test controls. Each Ping
// stands for one pool acquisition.
type stubPinger struct {
	mu    sync.Mutex
	err   error
	calls atomic.Int32
	// delay is how long each Ping takes, unless ctx ends first.
	delay time.Duration
}

func (s *stubPinger) Ping(ctx context.Context) error {
	s.calls.Add(1)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stubPinger) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newHandler(t *testing.T, db rpc.DBPinger) (*rpc.HealthCheckHandler, *fakeClock) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	h := rpc.NewHealthCheckHandler(db, logger)
	clock := &fakeClock{t: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)}
	h.SetClock(clock.now)
	return h, clock
}

func check(t *testing.T, ctx context.Context, h *rpc.HealthCheckHandler, service string) grpchealth.Status {
	t.Helper()
	status, err := checkStatus(ctx, h, service)
	require.NoError(t, err)
	return status
}

// checkStatus is check for use off the test goroutine.
func checkStatus(ctx context.Context, h *rpc.HealthCheckHandler, service string) (grpchealth.Status, error) {
	resp, err := h.Check(ctx, &grpchealth.CheckRequest{Service: service})
	if err != nil {
		return 0, err
	}
	return resp.Status, nil
}

func TestHealthCheckHandler_Liveness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		shuttingDown bool
	}{
		// @spec components/infrastructure/backend/process/health-probes "Database unreachable"
		{name: "alive while the database is unreachable"},
		{name: "alive while shutting down", shuttingDown: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := &stubPinger{err: errors.New("connection refused")}
			h, _ := newHandler(t, db)
			if tt.shuttingDown {
				h.SetShuttingDown()
			}

			assert.Equal(t, grpchealth.StatusServing, check(t, context.Background(), h, rpc.LivenessService))
			assert.Zero(t, db.calls.Load(), "liveness never touches the database")
		})
	}
}

// @spec components/infrastructure/backend/process/health-probes "Database unreachable"
func TestHealthCheckHandler_ReadinessFollowsDatabase(t *testing.T) {
	t.Parallel()
	db := &stubPinger{err: errors.New("connection refused")}
	h, clock := newHandler(t, db)
	ctx := context.Background()

	assert.Equal(t, grpchealth.StatusNotServing, check(t, ctx, h, ""))

	db.setErr(nil)
	assert.Equal(t, grpchealth.StatusNotServing, check(t, ctx, h, ""),
		"a result under 5 s old is reused")
	assert.Equal(t, int32(1), db.calls.Load())

	clock.advance(5 * time.Second)
	assert.Equal(t, grpchealth.StatusServing, check(t, ctx, h, ""),
		"ready again once the database is reachable")
	assert.Equal(t, int32(2), db.calls.Load())
}

// @spec components/infrastructure/backend/process/health-probes "Shutting down"
func TestHealthCheckHandler_ReadinessWhileShuttingDown(t *testing.T) {
	t.Parallel()
	db := &stubPinger{}
	h, _ := newHandler(t, db)

	require.NoError(t, h.Close())

	assert.Equal(t, grpchealth.StatusNotServing, check(t, context.Background(), h, ""))
	assert.Zero(t, db.calls.Load())
}

// @spec components/infrastructure/backend/process/health-probes "Concurrent checks against a slow database"
func TestHealthCheckHandler_ConcurrentChecksShareOneDatabaseCheck(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		db := &stubPinger{delay: 8 * time.Second}
		h, _ := newHandler(t, db)

		const probes = 10
		results := make(chan grpchealth.Status, probes)
		for range probes {
			go func() {
				status, _ := checkStatus(context.Background(), h, "")
				results <- status
			}()
		}

		for range probes {
			assert.Equal(t, grpchealth.StatusServing, <-results)
		}
		assert.Equal(t, int32(1), db.calls.Load(), "one database check, one pool acquisition")
	})
}

func TestHealthCheckHandler_ProbeTimeoutDoesNotAbandonCheck(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		db := &stubPinger{delay: 8 * time.Second}
		h, _ := newHandler(t, db)

		// The probe gives up after 5 s, before the database answers: not
		// ready, since no check has completed yet.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.Equal(t, grpchealth.StatusNotServing, check(t, ctx, h, ""))

		// The check kept running under its own context; the next probe joins
		// it instead of starting a second one.
		assert.Equal(t, grpchealth.StatusServing, check(t, context.Background(), h, ""))
		assert.Equal(t, int32(1), db.calls.Load())
	})
}
