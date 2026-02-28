package server

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// HealthServer provides a lightweight HTTP server for Kubernetes health probes.
// It exposes /healthz (liveness) and /readyz (readiness) endpoints.
type HealthServer struct {
	srv          *http.Server
	shuttingDown atomic.Bool
}

// NewHealthServer creates a health probe server listening on the given address.
func NewHealthServer(addr string) *HealthServer {
	h := &HealthServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if h.shuttingDown.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("shutting down"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	h.srv = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return h
}

// Start begins listening and serving. It blocks until the server is stopped.
// It returns http.ErrServerClosed when Shutdown is called.
func (h *HealthServer) Start() error {
	ln, err := net.Listen("tcp", h.srv.Addr)
	if err != nil {
		return err
	}
	return h.srv.Serve(ln)
}

// SetShuttingDown transitions the readiness endpoint to return 503.
func (h *HealthServer) SetShuttingDown() {
	h.shuttingDown.Store(true)
}

// healthShutdownTimeout is the maximum time to wait for the health server
// to drain active connections. Health probes are lightweight, so 5 seconds
// is generous. This prevents the health server from blocking overall
// shutdown if a probe client holds a connection open.
const healthShutdownTimeout = 5 * time.Second

// Close transitions the readiness endpoint to 503 and gracefully stops
// the health server. It implements [io.Closer] so the server can be
// registered with the shutdown package's Drain phase.
func (h *HealthServer) Close() error {
	h.SetShuttingDown()
	ctx, cancel := context.WithTimeout(context.Background(), healthShutdownTimeout)
	defer cancel()
	return h.srv.Shutdown(ctx)
}
