package auth

import (
	"context"

	"connectrpc.com/connect"
)

// RequireRoleInterceptor enforces, at the server boundary, that every RPC caller
// holds the named Zitadel project role. It reads the bridged claims (see
// ClaimsBridgeInterceptor) and rejects callers without the role with
// CodePermissionDenied before any handler runs.
//
// It is applied server-wide on the admin Connect server so that admin RPCs are
// gated structurally rather than by per-method discipline: because the admin
// server hosts only admin services and this layer is server-wide, it is
// impossible to register an un-gated admin RPC. It must be ordered after the
// claims bridge (so the bridged claims are present) and before validation.
type RequireRoleInterceptor struct {
	role string
}

// NewRequireRoleInterceptor creates an interceptor that requires every caller to
// hold the given role.
func NewRequireRoleInterceptor(role string) RequireRoleInterceptor {
	return RequireRoleInterceptor{role: role}
}

// WrapUnary enforces the required role for unary RPCs.
func (i RequireRoleInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := RequireRole(ctx, i.role); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient is a no-op for client streaming.
func (i RequireRoleInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler enforces the required role for streaming RPCs.
func (i RequireRoleInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := RequireRole(ctx, i.role); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}
