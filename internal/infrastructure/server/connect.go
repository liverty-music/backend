// Package server provides HTTP server implementation with Connect-RPC support.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"log/slog"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/pkg/config"
	apperr_connect "github.com/pannpers/go-apperr/apperr/connect"
	"github.com/pannpers/go-logging/logging"
)

// ConnectServer represents the Connect server.
type ConnectServer struct {
	server  *http.Server
	logger  *logging.Logger
	Cfg     *config.Config
	address string
}

// RPCHandlerFunc is a function that returns a path and a handler for a Connect RPC service.
type RPCHandlerFunc func(opts ...connect.HandlerOption) (string, http.Handler)

// HealthHandlerFunc is a function that returns a path and handler for the health check endpoint.
type HealthHandlerFunc func(opts ...connect.HandlerOption) (string, http.Handler)

// NewConnectServer creates a new Connect server instance.
func NewConnectServer(
	cfg *config.Config,
	logger *logging.Logger,
	_ *rdb.Database,
	authFunc authn.AuthFunc,
	healthHandler HealthHandlerFunc,
	handlerFuncs ...RPCHandlerFunc,
) *ConnectServer {
	// Create interceptors
	tracingInterceptor, _ := otelconnect.NewInterceptor()
	accessLogInterceptor := logging.NewAccessLogInterceptor(logger)
	validationInterceptor := validate.NewInterceptor()

	// Interceptor chain — execution order matters.
	//
	// Connect-RPC applies HandlerOptions in registration order. Within a single
	// WithInterceptors() call, the first interceptor listed becomes the outermost
	// layer. When multiple HandlerOptions are registered, each subsequent option's
	// interceptors are appended inside the previous ones via chainWith().
	//
	// The enriched ctx created by an outer interceptor flows inward as a function
	// argument to next(ctx, req), so all inner interceptors automatically receive
	// context values (e.g., OTel span) set by outer ones.
	//
	// Execution order (outermost → innermost):
	//
	//   [1] tracingInterceptor        — Starts OTel span. ALL inner layers get trace_id/span_id.
	//   [2] accessLogInterceptor      — Logs after next() returns. Sees *connect.Error (converted
	//                                   by [3]) for correct status codes. Outside [4] so it is NOT
	//                                   bypassed when a panic unwinds the stack.
	//   [3] errorHandlingInterceptor  — Converts AppErr → *connect.Error. Has trace context from [1].
	//   [4] recoverHandler            — defer recover(). Returns *connect.Error on panic. Has trace
	//                                   context from [1]. Inside [2] so access log still fires.
	//   [5] claimsBridgeInterceptor   — Reads authn.infoKey (set by HTTP-layer authn.Middleware)
	//                                   and writes auth.claimsKey for handlers.
	//   [6] validationInterceptor     — Validates request proto via protovalidate. Innermost layer.
	//
	// Response path (innermost → outermost):
	//   handler error (AppErr) → [6] pass → [5] pass → [4] pass (or catch panic) →
	//   [3] convert to *connect.Error → [2] log with correct status → [1] end span
	//
	handlerOpts := []connect.HandlerOption{
		connect.WithInterceptors(
			tracingInterceptor,
			accessLogInterceptor,
			apperr_connect.NewErrorHandlingInterceptor(logger),
		),
		newRecoverHandler(logger),
		connect.WithInterceptors(
			auth.ClaimsBridgeInterceptor{},
			validationInterceptor,
		),
	}

	// Protected mux — all RPC services
	protectedMux := http.NewServeMux()
	for _, handlerFunc := range handlerFuncs {
		path, handler := handlerFunc(handlerOpts...)
		protectedMux.Handle(path, handler)
	}

	// Health check handler (no auth required for K8s probes)
	healthPath, healthH := healthHandler(handlerOpts...)

	// Wrap protected mux with authn middleware (default-deny)
	authMiddleware := authn.NewMiddleware(authFunc)

	// Root mux: health check is public, everything else requires auth
	rootMux := http.NewServeMux()
	rootMux.Handle(healthPath, healthH)
	rootMux.Handle("/", authMiddleware.Wrap(protectedMux))

	address := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))

	handler := NewCORSHandler(rootMux, cfg.Server.AllowedOrigins)

	// Enable h2c (HTTP/2 without TLS) for Kubernetes gRPC health probes
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)

	server := &http.Server{
		Addr:              address,
		Handler:           http.TimeoutHandler(handler, cfg.Server.HandlerTimeout, ""),
		Protocols:         p,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	return &ConnectServer{
		server:  server,
		logger:  logger,
		Cfg:     cfg,
		address: address,
	}
}

// Start starts the Connect server.
func (s *ConnectServer) Start() error {
	s.logger.Info(context.Background(), fmt.Sprintf("Connect Server starting on %s", s.address))

	return s.server.ListenAndServe()
}

// Stop gracefully stops the Connect server.
func (s *ConnectServer) Stop() error {
	if s.server != nil {
		timeout := s.Cfg.ShutdownTimeout

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		s.logger.Info(ctx, "Shutting down Connect server gracefully...", slog.Duration("timeout", timeout))

		return s.server.Shutdown(ctx)
	}

	return nil
}

func newRecoverHandler(logger *logging.Logger) connect.HandlerOption {
	return connect.WithRecover(func(ctx context.Context, spec connect.Spec, _ http.Header, p any) error {
		logger.Error(ctx, "Panic recovered in Connect handler", fmt.Errorf("panic: %v", p),
			slog.String("procedure", spec.Procedure),
		)

		return connect.NewError(connect.CodeInternal, fmt.Errorf("internal server error"))
	})
}
