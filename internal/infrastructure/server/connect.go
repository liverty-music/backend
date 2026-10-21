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
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	"connectrpc.com/otelconnect"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/liverty-music/backend/pkg/config"
	apperrconnect "github.com/pannpers/go-apperr/apperr/connectv2"
	"github.com/pannpers/go-logging/logging"
	loggingconnect "github.com/pannpers/go-logging/logging/connectv2"
)

// ConnectServer represents the Connect server.
type ConnectServer struct {
	server  *http.Server
	logger  *logging.Logger
	address string
}

// RPCHandlerFunc registers one Connect RPC service on a [connect.Server].
type RPCHandlerFunc func(server *connect.Server)

// HealthHandlerFunc registers the gRPC health check service on a
// [connect.Server].
type HealthHandlerFunc func(server *connect.Server)

// healthServicePath is the URL path prefix of the gRPC health check service
// that HealthHandlerFunc registers.
const healthServicePath = "/" + grpchealth.HealthV1ServiceName + "/"

// PublicHTTPRoute is a plain HTTP handler served on the Connect server's root
// mux, beside the health check and outside the authn middleware and the
// Connect interceptor chain (so no per-IP rate limit applies). It is only for
// endpoints that return data already public through an RPC, such as the link
// preview tags of an Event page.
type PublicHTTPRoute struct {
	// Pattern is the [http.ServeMux] pattern, e.g. "GET /link-preview/events/{id}".
	Pattern string
	// Handler serves the route; it is wrapped with the server's HandlerTimeout.
	Handler http.Handler
}

// NewConnectServer creates a new Connect server instance.
//
// It is the shared factory for both the consumer and the admin Connect servers:
// the two are built identically except for the port and CORS origins (carried by
// serverCfg), the handler set (handlerFuncs), and any
// extraInterceptors. The consumer server passes no extra interceptors; the admin
// server passes its boundary admin-authorization interceptor. extraInterceptors are
// inserted after the claims bridge and before validation, preserving the
// interceptor-chain-ordering invariants for both servers.
//
// Every RPC handler is wrapped with the server's HandlerTimeout.
// publicRoutes are served without authentication; see [PublicHTTPRoute].
func NewConnectServer(
	serverCfg config.ServerSettings,
	logger *logging.Logger,
	authFunc authn.AuthFunc,
	rateLimiter *ratelimit.Limiter,
	healthHandler HealthHandlerFunc,
	extraInterceptors []connect.ServerInterceptor,
	publicRoutes []PublicHTTPRoute,
	handlerFuncs ...RPCHandlerFunc,
) *ConnectServer {
	// Create interceptors. otelconnect only fails on invalid options, and none
	// are passed; on failure the server runs without tracing rather than not
	// at all, as the v1 code (which discarded the error) did.
	tracingInterceptor, err := otelconnect.NewServerInterceptor()
	if err != nil {
		logger.Error(context.Background(), "failed to create the tracing interceptor; serving without tracing", err)
		tracingInterceptor = passThroughInterceptor
	}
	accessLogInterceptor := loggingconnect.NewAccessLogInterceptor(logger)
	validationInterceptor := validate.NewServerInterceptor()

	// Interceptor chain — execution order matters.
	//
	// connect.NewServer applies its interceptors in argument order: the first
	// interceptor listed is the outermost layer. Each interceptor wraps every
	// RPC type (unary and streaming) through the same connect.ServerFunc;
	// interceptors that were unary-only in v1 (the rate limiters) check
	// spec.StreamType themselves and pass streams through.
	//
	// The enriched ctx created by an outer interceptor flows inward as a function
	// argument to next(ctx, spec, stream), so all inner interceptors automatically
	// receive context values (e.g., OTel span) set by outer ones.
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
	//   [5] recoverInterceptor        — defer recover(). Returns *connect.Error on panic. Has trace
	//                                   context from [1]. Inside [3] so access log still fires.
	//   [6] claimsBridgeInterceptor        — Reads authn.infoKey (set by HTTP-layer authn.Middleware)
	//                                        and writes auth.claimsKey for handlers.
	//   [6b] extraInterceptors             — Optional per-server layers (e.g. the admin server's
	//                                        REQUIRE-ADMIN authorization). Run after the claims bridge
	//                                        so they see bridged claims, before validation.
	//   [7] validationInterceptor          — Validates request proto via protovalidate. Innermost layer.
	//
	// Response path (innermost → outermost):
	//   handler error (AppErr) → [7] pass → [6b] pass → [6] pass → [5] pass (or catch panic) →
	//   [4] convert to *connect.Error → [3] log with correct status → [2] rate limit pass → [1] end span
	//
	// The claims bridge runs first so the bridged claims are visible to any
	// extraInterceptors; validation runs last so it is innermost.
	interceptors := []connect.ServerInterceptor{
		tracingInterceptor,
		ratelimit.NewInterceptor(rateLimiter),
		accessLogInterceptor,
		apperrconnect.NewErrorHandlingInterceptor(logger),
		newRecoverInterceptor(logger),
		auth.NewClaimsBridgeInterceptor(),
	}
	interceptors = append(interceptors, extraInterceptors...)
	interceptors = append(interceptors, validationInterceptor)

	// RPC services share one connect.Server, mounted on the protected mux.
	// connecthttp.Mount keeps v2's default 4 MiB per-message read limit.
	rpcServer := connect.NewServer(interceptors...)
	for _, handlerFunc := range handlerFuncs {
		handlerFunc(rpcServer)
	}
	protectedMux := http.NewServeMux()
	connecthttp.Mount(protectedMux, rpcServer)

	// Health check server — minimal chain for Kubernetes probes.
	// No access log, tracing, error-handling, auth bridge, or validation;
	// health checks are called every few seconds and those layers add
	// noise without operational value.
	healthServer := connect.NewServer(newRecoverInterceptor(logger))
	healthHandler(healthServer)
	healthMux := http.NewServeMux()
	connecthttp.Mount(healthMux, healthServer)

	// Wrap protected mux with authn middleware (default-deny)
	authMiddleware := authn.NewMiddleware(authFunc)

	// Root mux: health check is public, everything else requires auth
	rootMux := http.NewServeMux()
	rootMux.Handle(healthServicePath, http.TimeoutHandler(healthMux, serverCfg.HandlerTimeout, ""))
	for _, route := range publicRoutes {
		rootMux.Handle(route.Pattern, http.TimeoutHandler(route.Handler, serverCfg.HandlerTimeout, ""))
	}
	// Every RPC is wrapped with the standard HandlerTimeout.
	rootMux.Handle("/", authMiddleware.Wrap(http.TimeoutHandler(protectedMux, serverCfg.HandlerTimeout, "")))

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

// Handler returns the fully-wired HTTP handler (authn middleware, CORS,
// interceptor chain and every registered RPC/health route), without binding
// a listener. It exists so tests can exercise the exact request pipeline a
// call to Start would serve — e.g. wrapped in httptest.NewServer — without
// needing a live TCP port.
func (s *ConnectServer) Handler() http.Handler {
	return s.server.Handler
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

// newRecoverInterceptor returns a server interceptor that recovers a panic in
// any inner layer or handler, logs it, and fails the RPC with CodeInternal and
// the message "internal server error". It replaces v1's connect.WithRecover,
// which connect-go v2 does not have.
//
// It re-panics http.ErrAbortHandler, as v1's WithRecover did, so the HTTP
// server still aborts the response.
func newRecoverInterceptor(logger *logging.Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (err error) {
			defer func() {
				p := recover()
				if p == nil {
					return
				}
				if p == http.ErrAbortHandler { //nolint:errorlint // the sentinel is panicked as is, never wrapped
					panic(p)
				}
				logger.Error(ctx, "Panic recovered in Connect handler", fmt.Errorf("panic: %v", p),
					slog.String("procedure", spec.Procedure),
				)
				err = connect.NewError(connect.CodeInternal, "internal server error")
			}()
			return next(ctx, spec, stream)
		}
	}
}

// passThroughInterceptor is a server interceptor that does nothing.
func passThroughInterceptor(next connect.ServerFunc) connect.ServerFunc {
	return next
}
