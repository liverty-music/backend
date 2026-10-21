package auth

import (
	"context"

	"connectrpc.com/connect/v2"
)

// NewRequireRoleInterceptor returns a server interceptor that enforces, at the
// server boundary, that every RPC caller holds the named Zitadel project role.
// It reads the bridged claims (see NewClaimsBridgeInterceptor) and rejects,
// before any handler runs, an unauthenticated caller with CodeUnauthenticated
// and an authenticated caller missing the role with CodePermissionDenied (see
// RequireRole).
//
// It is applied server-wide on the admin Connect server so that admin RPCs are
// gated structurally rather than by per-method discipline: because the admin
// server hosts only admin services and this layer is server-wide, it is
// impossible to register an un-gated admin RPC. It must be ordered after the
// claims bridge (so the bridged claims are present) and before validation.
//
// It applies to every RPC type (unary and streaming), as the v1 interceptor
// did through its WrapUnary and WrapStreamingHandler methods.
func NewRequireRoleInterceptor(role string) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if err := RequireRole(ctx, role); err != nil {
				return err
			}
			return next(ctx, spec, stream)
		}
	}
}
