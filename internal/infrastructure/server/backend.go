package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/otelconnect"
	"connectrpc.com/validate"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	apperrconnect "github.com/pannpers/go-apperr/apperr/connectv2"
	"github.com/pannpers/go-logging/logging"
	loggingconnect "github.com/pannpers/go-logging/logging/connectv2"
	"golang.org/x/oauth2"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// BackendServerSettings is the listener of a server that other backend
// workloads call inside the cluster.
type BackendServerSettings struct {
	// Host and Port to bind to.
	Host string
	Port int
	// HandlerTimeout bounds one request.
	HandlerTimeout time.Duration
}

// backendReadHeaderTimeout bounds reading a request's headers. Callers are
// backend workloads in the cluster, so headers arrive at once.
const backendReadHeaderTimeout = 5 * time.Second

// NewBackendServer creates a Connect server for one calling backend workload.
// It serves only the procedures in allowed and answers PermissionDenied to any
// other, without calling a handler, so a procedure is closed to a caller
// unless listed for it. It has no sign-in, no CORS and no rate limit: the
// network policy admits only that caller to the port.
//
// A handler error caused by an unreachable identity provider is answered with
// Unavailable, so the caller can retry; every other error keeps its code.
//
// Interceptors, outermost first: tracing, access log, error handling, panic
// recovery, the procedure allowlist, the identity-provider outage mapping, and
// request validation.
func NewBackendServer(settings BackendServerSettings, logger *logging.Logger, allowed []string, handlerFuncs ...RPCHandlerFunc) *ConnectServer {
	tracingInterceptor, err := otelconnect.NewServerInterceptor()
	if err != nil {
		logger.Error(context.Background(), "failed to create the tracing interceptor; serving without tracing", err)
		tracingInterceptor = passThroughInterceptor
	}
	rpcServer := connect.NewServer(
		tracingInterceptor,
		loggingconnect.NewAccessLogInterceptor(logger),
		apperrconnect.NewErrorHandlingInterceptor(logger),
		newRecoverInterceptor(logger),
		newProcedureAllowlistInterceptor(allowed),
		identityProviderUnavailableInterceptor,
		validate.NewServerInterceptor(),
	)
	for _, handlerFunc := range handlerFuncs {
		handlerFunc(rpcServer)
	}
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	return newH2CServer(settings, http.TimeoutHandler(mux, settings.HandlerTimeout, ""), logger)
}

// NewHealthCheckServer creates a server that serves only the gRPC health
// check, for a process whose other listeners carry no health check.
func NewHealthCheckServer(settings BackendServerSettings, logger *logging.Logger, healthHandler HealthHandlerFunc) *ConnectServer {
	healthServer := connect.NewServer(newRecoverInterceptor(logger))
	healthHandler(healthServer)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, healthServer)
	return newH2CServer(settings, http.TimeoutHandler(mux, settings.HandlerTimeout, ""), logger)
}

// newH2CServer wraps handler in an HTTP server that speaks HTTP/1.1 and
// HTTP/2 without TLS, for in-cluster Connect clients and the kubelet's gRPC
// probes.
func newH2CServer(settings BackendServerSettings, handler http.Handler, logger *logging.Logger) *ConnectServer {
	address := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	return &ConnectServer{
		server: &http.Server{
			Addr:              address,
			Handler:           handler,
			Protocols:         p,
			ReadHeaderTimeout: backendReadHeaderTimeout,
		},
		logger:  logger,
		address: address,
	}
}

// newProcedureAllowlistInterceptor refuses, with PermissionDenied, every
// procedure not in allowed.
func newProcedureAllowlistInterceptor(allowed []string) connect.ServerInterceptor {
	set := make(map[string]bool, len(allowed))
	for _, p := range allowed {
		set[p] = true
	}
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if !set[spec.Procedure] {
				return apperr.New(codes.PermissionDenied, "procedure is not served to this caller",
					slog.String("procedure", spec.Procedure))
			}
			return next(ctx, spec, stream)
		}
	}
}

// identityProviderUnavailableInterceptor answers Unavailable when the handler
// failed because the identity provider could not be reached (see
// identityProviderUnreachable); every other error keeps its code.
func identityProviderUnavailableInterceptor(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		err := next(ctx, spec, stream)
		if err != nil && identityProviderUnreachable(err) {
			return apperr.Wrap(err, codes.Unavailable, "identity provider is unreachable")
		}
		return err
	}
}

// identityProviderUnreachable reports whether err comes from failing to reach
// the identity provider: its API answered gRPC Unavailable, the sign-in token
// request could not be sent, or the token endpoint answered a server error.
func identityProviderUnreachable(err error) bool {
	var grpcErr interface{ GRPCStatus() *status.Status }
	if errors.As(err, &grpcErr) {
		return grpcErr.GRPCStatus().Code() == grpccodes.Unavailable
	}
	if _, ok := errors.AsType[*url.Error](err); ok {
		return true
	}
	var tokenErr *oauth2.RetrieveError
	return errors.As(err, &tokenErr) && tokenErr.Response != nil && tokenErr.Response.StatusCode >= http.StatusInternalServerError
}

// Serve serves on ln, which the caller has already bound. It lets the caller
// report readiness once every listener is bound. A stop through Close returns
// nil.
func (s *ConnectServer) Serve(ln net.Listener) error {
	s.logger.Info(context.Background(), "Connect server serving", slog.String("address", ln.Addr().String()))
	if err := s.server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Address returns the address the server binds to.
func (s *ConnectServer) Address() string {
	return s.address
}
