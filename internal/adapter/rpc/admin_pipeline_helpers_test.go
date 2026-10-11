package rpc_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/stretchr/testify/require"
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
	call func(context.Context, *connect.Request[Req]) (*connect.Response[Res], error),
) (*connect.Response[Res], error) {
	t.Helper()
	next := connect.UnaryFunc(func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
		resp, err := call(ctx, r.(*connect.Request[Req]))
		if err != nil {
			return nil, err
		}
		return resp, nil
	})
	validator := validate.NewInterceptor()
	chain := auth.NewRequireRoleInterceptor("admin").WrapUnary(validator.WrapUnary(next))
	resp, err := chain(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	out, ok := resp.(*connect.Response[Res])
	require.True(t, ok, "unexpected response type %T", resp)
	return out, nil
}

// nonAdminCtx returns a context of a signed-in caller without the admin role.
func nonAdminCtx() context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: "fan-sub-1"})
}
