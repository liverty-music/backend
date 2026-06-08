// Package auth provides authentication and authorization infrastructure for the application.
package auth

import (
	"context"
	"errors"
	"slices"

	"connectrpc.com/connect"
)

// Claims represents JWT claims extracted from the token.
type Claims struct {
	// Sub is the subject claim (external_id/user ID from identity provider).
	Sub string
	// Email is the user's email address.
	Email string
	// Name is the user's display name.
	Name string
	// EmailVerified indicates whether the user's email address has been verified.
	// This is extracted from the email_verified private claim set by a Zitadel Action.
	// Defaults to false when the claim is absent (fail-closed).
	EmailVerified bool
	// Roles is the list of Zitadel project role names the caller holds.
	// Populated from both the global claim
	// "urn:zitadel:iam:org:project:roles" and any project-scoped claim
	// "urn:zitadel:iam:org:project:{id}:roles". Each claim is a JSON object
	// whose keys are role names; the values (org-id→domain maps) are ignored.
	// An empty slice means the token carries no role grants.
	Roles []string
}

// HasRole reports whether the caller holds the named Zitadel project role.
func (c *Claims) HasRole(role string) bool {
	return slices.Contains(c.Roles, role)
}

// TokenValidator validates JWT tokens and returns the claims.
type TokenValidator interface {
	ValidateToken(ctx context.Context, tokenString string) (*Claims, error)
}

// Subject returns the subject claim (external user ID from identity provider).
// This satisfies the ratelimit.SubjectProvider interface.
func (c *Claims) Subject() string {
	return c.Sub
}

// contextKey is a type-safe key for storing values in context.
type contextKey struct{}

// claimsKey is the context key for storing the authenticated user claims.
var claimsKey = contextKey{}

// WithClaims returns a new context with the given JWT claims.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// GetClaims retrieves the JWT claims from the context.
// Returns the claims and true if found, or nil and false if not found.
func GetClaims(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	return claims, ok
}

// GetUserID retrieves the user ID (sub claim) from the context.
// Returns the user ID and true if found, or empty string and false if not found.
// Deprecated: Use GetClaims instead for full claim access.
func GetUserID(ctx context.Context) (string, bool) {
	claims, ok := GetClaims(ctx)
	if !ok || claims == nil {
		return "", false
	}
	return claims.Sub, true
}

// RequireRole checks that the authenticated caller holds the named Zitadel
// project role. It returns a PermissionDenied connect error when claims are
// absent from the context or the role is not present in the token. Handlers
// that are restricted to internal admin callers must invoke this at the start
// of every method.
func RequireRole(ctx context.Context, role string) error {
	claims, ok := GetClaims(ctx)
	if !ok || claims == nil {
		return connect.NewError(connect.CodePermissionDenied, errors.New("caller is not authenticated"))
	}
	if !claims.HasRole(role) {
		return connect.NewError(connect.CodePermissionDenied, errors.New("caller does not hold the required role: "+role))
	}
	return nil
}
