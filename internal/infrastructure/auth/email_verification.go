package auth

import (
	"context"
	"errors"

	"connectrpc.com/connect"
)

// errEmailVerificationRequired is the sentinel error returned when a user's
// email address has not been verified.
var errEmailVerificationRequired = errors.New("email verification required")

// EmailVerificationInterceptor is a Connect-RPC interceptor that enforces
// email verification for authenticated users. Requests from public endpoints
// (nil claims) and machine users (empty email) are allowed through unchanged.
type EmailVerificationInterceptor struct{}

// WrapUnary enforces email verification for unary RPCs.
// It blocks requests from authenticated human users whose email is not yet verified.
func (EmailVerificationInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := checkEmailVerified(ctx); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient is a no-op for client-side streaming.
func (EmailVerificationInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler enforces email verification for server-side streaming RPCs.
// It blocks requests from authenticated human users whose email is not yet verified.
func (EmailVerificationInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := checkEmailVerified(ctx); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

// checkEmailVerified returns a connect.CodeUnauthenticated error if the context
// contains claims for a human user (non-empty email) whose email is unverified.
// Nil claims (public endpoint) and empty email (machine user) are allowed through.
func checkEmailVerified(ctx context.Context) error {
	claims, ok := GetClaims(ctx)
	if !ok || claims == nil {
		// Public endpoint: no claims present, allow through.
		return nil
	}
	if claims.Email == "" {
		// Machine user: no email claim, allow through.
		return nil
	}
	if !claims.EmailVerified {
		return connect.NewError(connect.CodeUnauthenticated, errEmailVerificationRequired)
	}
	return nil
}
