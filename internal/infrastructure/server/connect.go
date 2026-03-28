// Package server provides HTTP server implementation with Connect-RPC support.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"log/slog"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/liverty-music/backend/pkg/config"
	apperr_connect "github.com/pannpers/go-apperr/apperr/connect"
	"github.com/pannpers/go-logging/logging"
)

// ConnectServer represents the Connect server.
type ConnectServer struct {
	server  *http.Server
	logger  *logging.Logger
	address string
}

// RPCHandlerFunc is a function that returns a path and a handler for a Connect RPC service.
type RPCHandlerFunc func(opts ...connect.HandlerOption) (string, http.Handler)

// HealthHandlerFunc is a function that returns a path and handler for the health check endpoint.
type HealthHandlerFunc func(opts ...connect.HandlerOption) (string, http.Handler)

// LongTimeoutRPCHandler groups an RPC handler that requires a longer timeout
// than the default HandlerTimeout (e.g., ConcertService with Gemini API calls).
type LongTimeoutRPCHandler struct {
	HandlerFunc RPCHandlerFunc
	Timeout     time.Duration
}

// NewConnectServer creates a new Connect server instance.
// longTimeoutHandlers are wrapped with their own http.TimeoutHandler instead of the default.
func NewConnectServer(
	serverCfg config.ServerSettings,
	logger *logging.Logger,
	authFunc authn.AuthFunc,
	rateLimiter *ratelimit.Limiter,
	healthHandler HealthHandlerFunc,
	longTimeoutHandlers []LongTimeoutRPCHandler,
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
	//   [2] rateLimitInterceptor      — Rejects excess requests with CodeResourceExhausted.
	//                                   After tracing so rejections are traced; before access log
	//                                   so they are logged with correct status.
	//   [3] accessLogInterceptor      — Logs after next() returns. Sees *connect.Error (converted
	//                                   by [4]) for correct status codes. Outside [5] so it is NOT
	//                                   bypassed when a panic unwinds the stack.
	//   [4] errorHandlingInterceptor  — Converts AppErr → *connect.Error. Has trace context from [1].
	//   [5] recoverHandler            — defer recover(). Returns *connect.Error on panic. Has trace
	//                                   context from [1]. Inside [3] so access log still fires.
	//   [6] claimsBridgeInterceptor        — Reads authn.infoKey (set by HTTP-layer authn.Middleware)
	//                                        and writes auth.claimsKey for handlers.
	//   [7] validationInterceptor          — Validates request proto via protovalidate. Innermost layer.
	//
	// Response path (innermost → outermost):
	//   handler error (AppErr) → [7] pass → [6] pass → [5] pass (or catch panic) →
	//   [4] convert to *connect.Error → [3] log with correct status → [2] rate limit pass → [1] end span
	//
	handlerOpts := []connect.HandlerOption{
		connect.WithInterceptors(
			tracingInterceptor,
			ratelimit.NewInterceptor(rateLimiter),
			accessLogInterceptor,
			apperr_connect.NewErrorHandlingInterceptor(logger),
		),
		newRecoverHandler(logger),
		connect.WithInterceptors(
			auth.ClaimsBridgeInterceptor{},
			validationInterceptor,
		),
	}

	// Health check opts — minimal chain for Kubernetes probes.
	// No access log, tracing, error-handling, auth bridge, or validation;
	// health checks are called every few seconds and those layers add
	// noise without operational value.
	healthOpts := []connect.HandlerOption{
		newRecoverHandler(logger),
	}

	// Protected mux — all RPC services
	protectedMux := http.NewServeMux()

	// Long-timeout handlers get their own http.TimeoutHandler wrapping.
	for _, lth := range longTimeoutHandlers {
		path, handler := lth.HandlerFunc(handlerOpts...)
		protectedMux.Handle(path, http.TimeoutHandler(handler, lth.Timeout, ""))
	}

	// Default-timeout handlers — each wrapped with the standard HandlerTimeout.
	for _, handlerFunc := range handlerFuncs {
		path, handler := handlerFunc(handlerOpts...)
		protectedMux.Handle(path, http.TimeoutHandler(handler, serverCfg.HandlerTimeout, ""))
	}

	// Health check handler (no auth required for K8s probes)
	healthPath, healthH := healthHandler(healthOpts...)

	// Wrap protected mux with authn middleware (default-deny)
	authMiddleware := authn.NewMiddleware(authFunc)

	// Root mux: health check is public, everything else requires auth
	rootMux := http.NewServeMux()
	rootMux.Handle(healthPath, http.TimeoutHandler(healthH, serverCfg.HandlerTimeout, ""))
	rootMux.Handle("/", authMiddleware.Wrap(protectedMux))

	address := net.JoinHostPort(serverCfg.Host, strconv.Itoa(serverCfg.Port))

	handler := NewCORSHandler(rootMux, serverCfg.AllowedOrigins)

	// Enable h2c (HTTP/2 without TLS) for Kubernetes gRPC health probes
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		Protocols:         p,
		ReadHeaderTimeout: serverCfg.ReadHeaderTimeout,
		ReadTimeout:       serverCfg.ReadTimeout,
		IdleTimeout:       serverCfg.IdleTimeout,
	}

	return &ConnectServer{
		server:  server,
		logger:  logger,
		address: address,
	}
}

// Start starts the Connect server.
func (s *ConnectServer) Start() error {
	s.logger.Info(context.Background(), fmt.Sprintf("Connect Server starting on %s", s.address))

	return s.server.ListenAndServe()
}

// serverDrainTimeout is the maximum time the Connect server waits for
// in-flight requests to complete. This must be smaller than the total
// shutdown budget (ShutdownTimeout) so that subsequent phases (flush,
// external, observe, datastore) still have time to run.
const serverDrainTimeout = 15 * time.Second

// Close gracefully stops the Connect server, draining in-flight requests.
// It implements [io.Closer] so the server can be registered with the
// shutdown package's Drain phase.
func (s *ConnectServer) Close() error {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), serverDrainTimeout)
		defer cancel()

		s.logger.Info(ctx, "draining Connect server", slog.Duration("timeout", serverDrainTimeout))

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
