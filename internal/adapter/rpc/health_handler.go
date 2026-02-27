package rpc

import (
	"context"
	"log/slog"
	"sync/atomic"

	"connectrpc.com/grpchealth"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-logging/logging"
)

// HealthCheckHandler implements [grpchealth.Checker] with database ping
// and shutdown-aware state transitions.
//
// When SetShuttingDown is called, subsequent Check calls immediately return
// [grpchealth.StatusNotServing] without touching the database, causing
// Kubernetes readiness probes to fail and triggering endpoint removal.
type HealthCheckHandler struct {
	db           *rdb.Database
	logger       *logging.Logger
	shuttingDown atomic.Bool
}

// NewHealthCheckHandler creates a new health check handler.
func NewHealthCheckHandler(db *rdb.Database, logger *logging.Logger) *HealthCheckHandler {
	return &HealthCheckHandler{
		db:     db,
		logger: logger,
	}
}

// Close atomically transitions the handler to shutdown state.
// After this call, Check always returns StatusNotServing.
// It implements [io.Closer] so the handler can be registered with the
// shutdown package's Drain phase.
func (h *HealthCheckHandler) Close() error {
	h.SetShuttingDown()
	return nil
}

// SetShuttingDown atomically transitions the handler to shutdown state.
// After this call, Check always returns StatusNotServing.
func (h *HealthCheckHandler) SetShuttingDown() {
	h.shuttingDown.Store(true)
	h.logger.Info(context.Background(), "health check transitioned to NOT_SERVING (shutdown)")
}

// Check implements the [grpchealth.Checker] interface.
// It returns StatusNotServing immediately if the application is shutting down,
// otherwise it pings the database to verify connectivity.
func (h *HealthCheckHandler) Check(ctx context.Context, req *grpchealth.CheckRequest) (*grpchealth.CheckResponse, error) {
	if h.shuttingDown.Load() {
		return &grpchealth.CheckResponse{Status: grpchealth.StatusNotServing}, nil
	}

	service := req.Service

	if err := h.db.Ping(ctx); err != nil {
		h.logger.Error(ctx, "health check failed: database ping failed", err, slog.String("service", service))

		return &grpchealth.CheckResponse{Status: grpchealth.StatusNotServing}, nil
	}

	h.logger.Debug(ctx, "health check passed", slog.String("service", service))

	return &grpchealth.CheckResponse{Status: grpchealth.StatusServing}, nil
}
