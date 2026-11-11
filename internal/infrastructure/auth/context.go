// Package auth provides authentication and authorization infrastructure for the application.
package auth

import (
	"context"

	"connectrpc.com/connect/v2"

	"github.com/liverty-music/backend/pkg/authctx"
)

// TokenValidator validates JWT tokens and returns the claims.
type TokenValidator interface {
	ValidateToken(ctx context.Context, tokenString string) (*authctx.Claims, error)
}

// RequireRole checks that the authenticated caller holds the named Zitadel
// project role. It returns an Unauthenticated connect error when claims are
// absent from the context (the caller never authenticated), and a
// PermissionDenied connect error when claims are present but the role is not
// in the token (the caller authenticated but lacks the grant). Handlers that
// are restricted to internal admin callers must invoke this at the start of
// every method.
func RequireRole(ctx context.Context, role string) error {
	claims, ok := authctx.GetClaims(ctx)
	if !ok || claims == nil {
		return connect.NewError(connect.CodeUnauthenticated, "caller is not authenticated")
	}
	if !claims.HasRole(role) {
		return connect.NewError(connect.CodePermissionDenied, "caller does not hold the required role: "+role)
	}
	return nil
}
