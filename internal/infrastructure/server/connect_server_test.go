package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Procedures of the raw test service that newChainTestServer registers.
const (
	chainPublicProcedure  = "/test.v1.ChainService/Public"
	chainPrivateProcedure = "/test.v1.ChainService/Private"
)

// newChainTestServer builds a Connect server through server.NewConnectServer
// (the production interceptor chain, authn middleware and health route) with
// one raw unary test service. Both procedures return whatever handlerErr
// returns; chainPublicProcedure is public, chainPrivateProcedure needs a
// sign-in.
func newChainTestServer(t *testing.T, handlerErr func() error) *httptest.Server {
	t.Helper()

	logger, err := logging.New()
	require.NoError(t, err)

	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{AuthRPS: 100, AuthBurst: 100, AnonRPS: 100, AnonBurst: 100}, time.Minute)
	t.Cleanup(func() { _ = rateLimiter.Close() })

	authFunc := auth.NewAuthFunc(authmocks.NewMockTokenValidator(t), map[string]bool{chainPublicProcedure: true})

	handler := func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
		if err := stream.Receive(&emptypb.Empty{}); err != nil {
			return err
		}
		if err := handlerErr(); err != nil {
			return err
		}
		return stream.Send(&emptypb.Empty{})
	}
	register := func(s *connect.Server) {
		s.Register(
			connect.Method{Spec: connect.Spec{Procedure: chainPublicProcedure, StreamType: connect.StreamTypeUnary}, Handler: handler},
			connect.Method{Spec: connect.Spec{Procedure: chainPrivateProcedure, StreamType: connect.StreamTypeUnary}, Handler: handler},
		)
	}
	healthHandler := func(s *connect.Server) {
		grpchealth.Register(s, grpchealth.NewStaticChecker())
	}
	cfg := config.ServerSettings{
		Host:              "127.0.0.1",
		HandlerTimeout:    5 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	srv := server.NewConnectServer(cfg, logger, authFunc, rateLimiter, healthHandler, nil, nil, register)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// callChain makes one unary call to procedure and returns the client error.
func callChain(t *testing.T, ts *httptest.Server, procedure string) error {
	t.Helper()
	client := connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL))
	spec := connect.Spec{Procedure: procedure, StreamType: connect.StreamTypeUnary}
	return client.CallUnary(context.Background(), spec, &emptypb.Empty{}, &emptypb.Empty{})
}

// TestConnectServer_ErrorCodes verifies that an error returned by a handler
// reaches the client with the code and message it had under connect-go v1: an
// apperr keeps its code, with its Error() text for a client error and
// "internal server error" for a server error; a local *connect.Error keeps its
// code and message; any other error becomes Unknown.
func TestConnectServer_ErrorCodes(t *testing.T) {
	t.Parallel()

	// remoteErr stands in for an error a downstream Connect client returned.
	// connect-go v2 sends a returned remote error as Internal, so a caller must
	// wrap it in an apperr, whose code then wins.
	remoteErr := connect.NewError(connect.CodeNotFound, "downstream not found").WithRemote()

	type want struct {
		code    connect.Code
		message string
	}
	tests := []struct {
		name       string
		handlerErr error
		want       want
	}{
		{name: "apperr InvalidArgument", handlerErr: apperr.New(codes.InvalidArgument, "bad input"), want: want{connect.CodeInvalidArgument, "bad input (invalid_argument)"}},
		{name: "apperr NotFound wrapped with fmt.Errorf", handlerErr: fmt.Errorf("get: %w", apperr.New(codes.NotFound, "no such thing")), want: want{connect.CodeNotFound, "no such thing (not_found)"}},
		{name: "apperr AlreadyExists", handlerErr: apperr.New(codes.AlreadyExists, "exists"), want: want{connect.CodeAlreadyExists, "exists (already_exists)"}},
		{name: "apperr PermissionDenied", handlerErr: apperr.New(codes.PermissionDenied, "denied"), want: want{connect.CodePermissionDenied, "denied (permission_denied)"}},
		{name: "apperr FailedPrecondition", handlerErr: apperr.New(codes.FailedPrecondition, "not yet"), want: want{connect.CodeFailedPrecondition, "not yet (failed_precondition)"}},
		{name: "apperr ResourceExhausted", handlerErr: apperr.New(codes.ResourceExhausted, "slow down"), want: want{connect.CodeResourceExhausted, "slow down (resource_exhausted)"}},
		{name: "apperr Unauthenticated", handlerErr: apperr.New(codes.Unauthenticated, "who"), want: want{connect.CodeUnauthenticated, "who (unauthenticated)"}},
		{name: "apperr Internal hides its message", handlerErr: apperr.New(codes.Internal, "db down"), want: want{connect.CodeInternal, "internal server error"}},
		{name: "apperr Unavailable hides its message", handlerErr: apperr.New(codes.Unavailable, "vendor down"), want: want{connect.CodeUnavailable, "internal server error"}},
		{name: "local connect error", handlerErr: connect.NewError(connect.CodeInvalidArgument, "endpoint is required"), want: want{connect.CodeInvalidArgument, "endpoint is required"}},
		{name: "remote connect error wrapped in apperr", handlerErr: apperr.Wrap(remoteErr, codes.NotFound, "lookup"), want: want{connect.CodeNotFound, ""}},
		{name: "plain error", handlerErr: errors.New("boom"), want: want{connect.CodeUnknown, "internal server error"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := newChainTestServer(t, func() error { return tt.handlerErr })

			err := callChain(t, ts, chainPublicProcedure)

			var connectErr *connect.Error
			require.ErrorAs(t, err, &connectErr)
			assert.Equal(t, tt.want.code, connectErr.Code())
			if tt.want.message != "" {
				assert.Equal(t, tt.want.message, connectErr.Message())
			}
		})
	}
}

func TestConnectServer_RecoversPanics(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	ts := newChainTestServer(t, func() error {
		if calls.Add(1) == 1 {
			panic("handler bug")
		}
		return nil
	})

	err := callChain(t, ts, chainPublicProcedure)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInternal, connectErr.Code())
	assert.Equal(t, "internal server error", connectErr.Message())

	assert.NoError(t, callChain(t, ts, chainPublicProcedure), "the server keeps serving after a panic")
}

func TestConnectServer_RequiresSignInOutsidePublicProcedures(t *testing.T) {
	t.Parallel()
	ts := newChainTestServer(t, func() error { return nil })

	assert.NoError(t, callChain(t, ts, chainPublicProcedure))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(callChain(t, ts, chainPrivateProcedure)))
}

func TestConnectServer_ServesHealthCheckWithoutSignIn(t *testing.T) {
	t.Parallel()
	ts := newChainTestServer(t, func() error { return nil })

	client := grpchealth.NewClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL, connecthttp.WithGRPC())))
	resp, err := client.Check(context.Background(), &grpchealth.CheckRequest{})

	require.NoError(t, err)
	assert.Equal(t, grpchealth.StatusServing, resp.Status)
}
