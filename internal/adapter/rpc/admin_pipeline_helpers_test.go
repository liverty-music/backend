package rpc_test

import (
	"context"
	"io"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// callThroughAdminPipeline runs one unary handler method behind the same
// interceptors as the admin server (internal/di/provider.go): the server-wide
// RequireRoleInterceptor for the admin role, then the protovalidate
// interceptor. It lets a handler test cover the role and boundary checks the
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
	chain := auth.NewRequireRoleInterceptor("admin")(validate.NewServerInterceptor()(handler))
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
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: "fan-sub-1"})
}
