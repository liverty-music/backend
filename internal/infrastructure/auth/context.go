// Package auth provides authentication and authorization infrastructure for the application.
package auth

import "context"

// Claims represents JWT claims extracted from the token.
type Claims struct {
	// Sub is the subject claim (external_id/user ID from identity provider).
	Sub string
	// Email is the user's email address.
	Email string
	// Name is the user's display name.
	Name string
}

// TokenValidator validates JWT tokens and returns the claims.
type TokenValidator interface {
	ValidateToken(ctx context.Context, tokenString string) (*Claims, error)
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
