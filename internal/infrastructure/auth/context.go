// Package auth provides authentication and authorization infrastructure for the application.
package auth

import "context"

// contextKey is a type-safe key for storing values in context.
type contextKey struct{}

// userIDKey is the context key for storing the authenticated user ID.
var userIDKey = contextKey{}

// WithUserID returns a new context with the given user ID.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// GetUserID retrieves the user ID from the context.
// Returns the user ID and true if found, or empty string and false if not found.
func GetUserID(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(userIDKey).(string)
	return userID, ok
}
