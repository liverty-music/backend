package auth

import (
	"context"
	"fmt"
	"strings"

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
}

// TokenValidator validates JWT tokens and returns the claims.
type TokenValidator interface {
	ValidateToken(ctx context.Context, tokenString string) (*Claims, error)
}

// AuthInterceptor is a Connect-RPC interceptor that validates JWT tokens.
//
//nolint:revive // AuthInterceptor is intentionally prefixed with "Auth" for clarity
type AuthInterceptor struct {
	validator TokenValidator
}

// NewAuthInterceptor creates a new auth interceptor.
func NewAuthInterceptor(validator TokenValidator) *AuthInterceptor {
	return &AuthInterceptor{
		validator: validator,
	}
}

// WrapUnary wraps a unary RPC handler with authentication.
// If no Authorization header is present, the request proceeds without authentication (for public endpoints).
// If an Authorization header is present, it must be valid or the request fails.
func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// Extract Authorization header
		authHeader := req.Header().Get("Authorization")
		if authHeader == "" {
			// No auth header - allow request to proceed (public endpoint)
			return next(ctx, req)
		}

		// Extract Bearer token
		const bearerPrefix = "Bearer "
		if !strings.HasPrefix(authHeader, bearerPrefix) {
			return nil, connect.NewError(
				connect.CodeUnauthenticated,
				fmt.Errorf("authorization header must use Bearer scheme"),
			)
		}

		tokenString := strings.TrimPrefix(authHeader, bearerPrefix)
		if tokenString == "" {
			return nil, connect.NewError(
				connect.CodeUnauthenticated,
				fmt.Errorf("authorization token is empty"),
			)
		}

		// Validate token and extract claims
		claims, err := i.validator.ValidateToken(ctx, tokenString)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}

		// Add claims to context
		ctx = WithClaims(ctx, claims)

		// Call next handler with authenticated context
		return next(ctx, req)
	}
}

// WrapStreamingClient wraps a streaming client with authentication.
func (i *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next // No auth for client streaming
}

// WrapStreamingHandler wraps a streaming handler with authentication.
// If no Authorization header is present, the request proceeds without authentication (for public endpoints).
// If an Authorization header is present, it must be valid or the request fails.
func (i *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		// Extract Authorization header
		authHeader := conn.RequestHeader().Get("Authorization")
		if authHeader == "" {
			// No auth header - allow request to proceed (public endpoint)
			return next(ctx, conn)
		}

		// Extract Bearer token
		const bearerPrefix = "Bearer "
		if !strings.HasPrefix(authHeader, bearerPrefix) {
			return connect.NewError(
				connect.CodeUnauthenticated,
				fmt.Errorf("authorization header must use Bearer scheme"),
			)
		}

		tokenString := strings.TrimPrefix(authHeader, bearerPrefix)
		if tokenString == "" {
			return connect.NewError(
				connect.CodeUnauthenticated,
				fmt.Errorf("authorization token is empty"),
			)
		}

		// Validate token and extract claims
		claims, err := i.validator.ValidateToken(ctx, tokenString)
		if err != nil {
			return connect.NewError(connect.CodeUnauthenticated, err)
		}

		// Add claims to context
		ctx = WithClaims(ctx, claims)

		// Call next handler with authenticated context
		return next(ctx, conn)
	}
}
