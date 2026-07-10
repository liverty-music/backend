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
// The server starts in a "not ready" state; call SetReady after the
// application has finished initialization.
type HealthServer struct {
	srv          *http.Server
	ready        atomic.Bool
	shuttingDown atomic.Bool
	// liveness, when set, gates /healthz: it reports unhealthy (503) when the
	// probe returns false. It is stored behind an atomic so it can be installed
	// after the server is already serving (the probe server starts before DI so
	// Kubernetes can observe the pod during initialization). Until set, /healthz
	// reports healthy so a booting pod is not killed before it is ready.
	liveness atomic.Pointer[func() bool]
}

// NewHealthServer creates a health probe server listening on the given address.
func NewHealthServer(addr string) *HealthServer {
	h := &HealthServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if probe := h.liveness.Load(); probe != nil && !(*probe)() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("unhealthy"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if h.shuttingDown.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("shutting down"))
			return
		}
		if !h.ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
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

// Handler returns the HTTP handler serving the probe endpoints. It is exposed
// so the endpoints can be exercised in tests without binding a socket.
func (h *HealthServer) Handler() http.Handler {
	return h.srv.Handler
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

// SetReady transitions the readiness endpoint from 503 to 200,
// indicating that application initialization is complete.
func (h *HealthServer) SetReady() {
	h.ready.Store(true)
}

// SetLiveness installs a liveness probe for /healthz. Once set, /healthz
// reports unhealthy (503) whenever probe returns false, so Kubernetes restarts
// a pod that is no longer consuming. Passing nil clears the probe.
func (h *HealthServer) SetLiveness(probe func() bool) {
	if probe == nil {
		h.liveness.Store(nil)
		return
	}
	h.liveness.Store(&probe)
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
