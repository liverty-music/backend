package rpc_test

import (
	"context"
	"io"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/pkg/authctx"
	apperrconnect "github.com/pannpers/go-apperr/apperr/connectv2"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// callThroughAdminPipeline runs one unary handler method behind the same
// interceptors as the admin server (internal/di/provider.go and
// internal/infrastructure/server/connect.go): the apperr error-handling
// interceptor that turns an apperr code into its Connect code, the
// server-wide RequireRoleInterceptor for the admin role, then the
// protovalidate interceptor. It lets a handler test cover the role and boundary checks the
// admin adapter specs state without starting a server.
func callThroughAdminPipeline[Req, Res any](
	t *testing.T,
	ctx context.Context,
	req *Req,
	call func(context.Context, *Req) (*Res, error),
) (*Res, error) {
	t.Helper()
	handler := connect.ServerFunc(func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
		in := new(Req)
		if err := stream.Receive(in); err != nil {
			return err
		}
		resp, err := call(ctx, in)
		if err != nil {
			return err
		}
		return stream.Send(resp)
	})
	logger, err := logging.New()
	require.NoError(t, err)
	chain := apperrconnect.NewErrorHandlingInterceptor(logger)(
		auth.NewRequireRoleInterceptor("admin")(validate.NewServerInterceptor()(handler)))
	reqMsg, ok := any(req).(proto.Message)
	require.True(t, ok, "request type %T is not a proto message", req)
	stream := &unaryTestStream{req: reqMsg}
	spec := connect.Spec{Procedure: "/test.v1.AdminService/Method", StreamType: connect.StreamTypeUnary}
	if err := chain(ctx, spec, stream); err != nil {
		return nil, err
	}
	out, ok := stream.resp.(*Res)
	require.True(t, ok, "unexpected response type %T", stream.resp)
	return out, nil
}

// unaryTestStream is a connect.ServerStream that delivers one request message
// and records the response message.
type unaryTestStream struct {
	req      proto.Message
	received bool
	resp     any
}

func (s *unaryTestStream) Receive(msg any) error {
	if s.received {
		return io.EOF
	}
	s.received = true
	dst, ok := msg.(proto.Message)
	if !ok {
		return connect.Errorf(connect.CodeInternal, "message type %T is not a proto message", msg)
	}
	proto.Merge(dst, s.req)
	return nil
}

func (s *unaryTestStream) SendHeaders() error { return nil }

func (s *unaryTestStream) Send(msg any) error {
	s.resp = msg
	return nil
}

// nonAdminCtx returns a context of a signed-in caller without the admin role.
func nonAdminCtx() context.Context {
	return authctx.WithClaims(context.Background(), &authctx.Claims{Sub: "fan-sub-1"})
}
