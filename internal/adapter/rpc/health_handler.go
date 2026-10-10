package rpc

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/grpchealth"
	"github.com/pannpers/go-logging/logging"
	"golang.org/x/sync/singleflight"
)

// LivenessService is the gRPC health service name the kubelet liveness and
// startup probes ask for. It reports whether the process can answer, never the
// state of a dependency, so a dependency outage does not restart the process.
const LivenessService = "liveness"

const (
	// readinessTTL is how long a completed database check answers readiness
	// probes before a new one runs.
	readinessTTL = 5 * time.Second
	// readinessCheckTimeout bounds one database check. The check runs under
	// its own context, not the probe's, so a probe timeout never abandons a
	// pool acquisition halfway.
	readinessCheckTimeout = 10 * time.Second
)

// DBPinger checks that the database answers. The handler needs only this from
// the connection pool.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// HealthCheckHandler implements [grpchealth.Checker].
//
// The LivenessService always reports SERVING, including while shutting down,
// so a draining pod is not killed early.
//
// Any other service name (the empty name is what kubelet readiness and the
// Gateway health check ask for) reports readiness: NOT_SERVING while shutting
// down or while the database is unreachable. Readiness never adds database
// load: at most one database check runs at a time, its result is reused for
// readinessTTL, and concurrent probes share the check in flight. A probe that
// ends before the check completes answers from the last completed result, or
// NOT_SERVING before any result exists.
type HealthCheckHandler struct {
	db           DBPinger
	logger       *logging.Logger
	now          func() time.Time
	shuttingDown atomic.Bool

	checks singleflight.Group
	mu     sync.Mutex
	last   readinessResult
}

// readinessResult is a completed database check.
type readinessResult struct {
	serving bool
	at      time.Time // zero before the first check completes
}

// NewHealthCheckHandler creates a new health check handler.
func NewHealthCheckHandler(db DBPinger, logger *logging.Logger) *HealthCheckHandler {
	return &HealthCheckHandler{
		db:     db,
		logger: logger,
		now:    time.Now,
	}
}

// Close atomically transitions the handler to shutdown state.
// After this call, readiness always reports StatusNotServing.
// It implements [io.Closer] so the handler can be registered with the
// shutdown package's Drain phase.
func (h *HealthCheckHandler) Close() error {
	h.SetShuttingDown()
	return nil
}

// SetShuttingDown atomically transitions the handler to shutdown state.
// After this call, readiness always reports StatusNotServing.
func (h *HealthCheckHandler) SetShuttingDown() {
	h.shuttingDown.Store(true)
	h.logger.Info(context.Background(), "health check transitioned to NOT_SERVING (shutdown)")
}

// Check implements the [grpchealth.Checker] interface.
func (h *HealthCheckHandler) Check(ctx context.Context, req *grpchealth.CheckRequest) (*grpchealth.CheckResponse, error) {
	if req.Service == LivenessService {
		return &grpchealth.CheckResponse{Status: grpchealth.StatusServing}, nil
	}
	if h.shuttingDown.Load() || !h.ready(ctx) {
		return &grpchealth.CheckResponse{Status: grpchealth.StatusNotServing}, nil
	}
	return &grpchealth.CheckResponse{Status: grpchealth.StatusServing}, nil
}

// ready answers from a fresh result, or joins (or starts) the database check
// and waits for it until ctx ends.
func (h *HealthCheckHandler) ready(ctx context.Context) bool {
	h.mu.Lock()
	last := h.last
	h.mu.Unlock()
	if !last.at.IsZero() && h.now().Sub(last.at) < readinessTTL {
		return last.serving
	}

	done := h.checks.DoChan("db", func() (any, error) {
		return h.checkDB(), nil
	})
	select {
	case r := <-done:
		return r.Val.(bool)
	case <-ctx.Done():
		return last.serving
	}
}

// checkDB pings the database under its own timeout and records the result.
func (h *HealthCheckHandler) checkDB() bool {
	ctx, cancel := context.WithTimeout(context.Background(), readinessCheckTimeout)
	defer cancel()

	err := h.db.Ping(ctx)
	if err != nil {
		h.logger.Error(ctx, "readiness check failed: database ping failed", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = readinessResult{serving: err == nil, at: h.now()}
	return h.last.serving
}
