package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"time"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
)

// DebugServer is a dedicated, internal-only HTTP server that exposes the
// `net/http/pprof` endpoints — including the Go 1.27 GA `goroutineleak`
// profile at `/debug/pprof/goroutineleak`.
//
// It listens on its own port (config.GoroutineLeakConfig.Port, default 6060),
// physically separate from the public Connect-RPC / admin / webhook / health
// listeners. That port is deliberately NOT added to any Kubernetes Service,
// Gateway, or HTTPRoute, so the profiling surface is unreachable from the
// public ingress. Operators pull profiles over an internal access path only
// (e.g. `kubectl port-forward` to the pod), satisfying the goroutine-leak
// spec's "operator/internal access, not a public unauthenticated route"
// requirement.
//
// The handlers are mounted on a private mux rather than http.DefaultServeMux,
// so importing net/http/pprof (which registers on DefaultServeMux via init)
// cannot leak these endpoints onto any other listener in the process.
type DebugServer struct {
	server  *http.Server
	logger  *logging.Logger
	address string
}

// NewDebugServer builds the pprof debug listener bound to cfg.Host:cfg.Port.
func NewDebugServer(cfg config.GoroutineLeakConfig, logger *logging.Logger) *DebugServer {
	mux := http.NewServeMux()
	// pprof.Index dispatches every runtime/pprof profile registered by name,
	// including "goroutineleak", so /debug/pprof/goroutineleak is served here.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	return &DebugServer{
		server: &http.Server{
			Addr:              address,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
		logger:  logger,
		address: address,
	}
}

// Handler returns the pprof mux. Exposed so the routing can be exercised in
// tests without binding a socket.
func (s *DebugServer) Handler() http.Handler {
	return s.server.Handler
}

// Start begins listening. It blocks until the server is stopped and returns
// http.ErrServerClosed on graceful shutdown.
func (s *DebugServer) Start() error {
	s.logger.Info(context.Background(), fmt.Sprintf("Debug (pprof) server starting on %s", s.address))
	return s.server.ListenAndServe()
}

// debugDrainTimeout bounds how long Close waits for in-flight profile pulls.
const debugDrainTimeout = 10 * time.Second

// Close gracefully stops the server. It implements [io.Closer] so the server
// can be registered with the shutdown package's Drain phase.
func (s *DebugServer) Close() error {
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), debugDrainTimeout)
	defer cancel()
	s.logger.Info(ctx, "draining Debug server", slog.Duration("timeout", debugDrainTimeout))
	return s.server.Shutdown(ctx)
}
