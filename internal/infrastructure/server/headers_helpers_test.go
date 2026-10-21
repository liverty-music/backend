package server_test

import (
	"context"

	"connectrpc.com/connect/v2"
)

// withHeader returns ctx carrying a client call whose request header key is
// set to value. It reuses the client call info already on ctx, so several
// headers can be set on one call.
func withHeader(ctx context.Context, key, value string) context.Context {
	info, ok := connect.CallInfoForClientContext(ctx)
	if !ok {
		ctx, info = connect.NewClientContext(ctx)
	}
	info.RequestHeader().Set(key, value)
	return ctx
}

// withBearer returns ctx carrying a client call with the bearer token, or ctx
// unchanged when token is empty (a caller who is not signed in).
func withBearer(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return withHeader(ctx, "Authorization", "Bearer "+token)
}
