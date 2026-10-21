package ratelimit_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/authn"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Test procedures served by newTestRPCServer.
const (
	unaryProcedure        = "/test.v1.TestService/Unary"
	serverStreamProcedure = "/test.v1.TestService/ServerStream"
)

var (
	unarySpec        = connect.Spec{Procedure: unaryProcedure, StreamType: connect.StreamTypeUnary}
	serverStreamSpec = connect.Spec{Procedure: serverStreamProcedure, StreamType: connect.StreamTypeServer}
)

// subjectHeader is a test-only request header that the test server turns into
// authn info, standing in for the authn middleware that sets it in production.
const subjectHeader = "X-Test-Subject"

// testSubject satisfies ratelimit.SubjectProvider.
type testSubject string

func (s testSubject) Subject() string { return string(s) }

// testRPCServer is a Connect server over HTTP that serves a unary and a
// server-streaming procedure behind the given interceptors.
type testRPCServer struct {
	client *connect.Client
}

// newTestRPCServer starts an httptest server whose Connect server runs the
// given interceptors around two procedures. handlerErr, when non-nil, decides
// the error each handler returns.
func newTestRPCServer(t *testing.T, handlerErr func() error, interceptors ...connect.ServerInterceptor) *testRPCServer {
	t.Helper()

	// The first interceptor bridges the test subject header to authn info,
	// as authn.Middleware does in production.
	withAuthn := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if info, ok := connect.CallInfoForServerContext(ctx); ok {
				if sub := info.RequestHeader().Get(subjectHeader); sub != "" {
					ctx = authn.SetInfo(ctx, testSubject(sub))
				}
			}
			return next(ctx, spec, stream)
		}
	}
	server := connect.NewServer(append([]connect.ServerInterceptor{withAuthn}, interceptors...)...)
	server.Register(
		connect.Method{Spec: unarySpec, Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			if err := stream.Receive(&emptypb.Empty{}); err != nil {
				return err
			}
			if handlerErr != nil {
				if err := handlerErr(); err != nil {
					return err
				}
			}
			return stream.Send(&emptypb.Empty{})
		}},
		connect.Method{Spec: serverStreamSpec, Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			if err := stream.Receive(&emptypb.Empty{}); err != nil {
				return err
			}
			if handlerErr != nil {
				if err := handlerErr(); err != nil {
					return err
				}
			}
			return stream.Send(&emptypb.Empty{})
		}},
	)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	return &testRPCServer{
		client: connect.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL)),
	}
}

// callOptions are the request headers of one test call.
type callOptions struct {
	forwardedFor string
	subject      string
}

// callUnary makes one unary call and returns its response headers and error.
func (s *testRPCServer) callUnary(t *testing.T, opts callOptions) (*connect.Header, error) {
	t.Helper()

	ctx, info := connect.NewClientContext(t.Context())
	setRequestHeaders(info, opts)
	err := s.client.CallUnary(ctx, unarySpec, &emptypb.Empty{}, &emptypb.Empty{})
	return info.ResponseHeader(), err
}

// callServerStream makes one server-streaming call, reads it to the end and
// returns its error.
func (s *testRPCServer) callServerStream(t *testing.T, opts callOptions) error {
	t.Helper()

	ctx, info := connect.NewClientContext(t.Context())
	setRequestHeaders(info, opts)
	stream, err := s.client.CallServerStream(ctx, serverStreamSpec, &emptypb.Empty{})
	if err != nil {
		return err
	}
	defer func() { require.NoError(t, stream.Close()) }()
	for {
		if err := stream.Receive(&emptypb.Empty{}); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// setRequestHeaders applies opts to the request headers of a call.
func setRequestHeaders(info *connect.CallInfo, opts callOptions) {
	if opts.forwardedFor != "" {
		info.RequestHeader().Set("X-Forwarded-For", opts.forwardedFor)
	}
	if opts.subject != "" {
		info.RequestHeader().Set(subjectHeader, opts.subject)
	}
}
