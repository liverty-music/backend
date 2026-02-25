package auth

import (
	"context"
	"net/http"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
)

// NewAuthFunc creates an authn.AuthFunc that validates JWT bearer tokens
// using the provided TokenValidator and returns *Claims on success.
//
// Procedures listed in publicProcedures are accessible without authentication.
// If a public procedure receives a valid bearer token, the claims are still
// returned so that downstream handlers can identify the caller. If the token
// is invalid or absent, the request passes through with nil info.
func NewAuthFunc(validator TokenValidator, publicProcedures map[string]bool) authn.AuthFunc {
	return func(ctx context.Context, req *http.Request) (any, error) {
		procedure, _ := authn.InferProcedure(req.URL)
		isPublic := publicProcedures[procedure]

		token, hasToken := authn.BearerToken(req)
		if !hasToken {
			if isPublic {
				return nil, nil
			}
			return nil, authn.Errorf("missing bearer token")
		}

		claims, err := validator.ValidateToken(ctx, token)
		if err != nil {
			if isPublic {
				return nil, nil
			}
			return nil, authn.Errorf("invalid token: %w", err)
		}

		return claims, nil
	}
}

// ClaimsBridgeInterceptor reads authn.GetInfo and injects *Claims into
// the context via WithClaims, preserving backward compatibility with
// handlers that use GetUserID/GetClaims.
type ClaimsBridgeInterceptor struct{}

// WrapUnary bridges authn info to auth.Claims context for unary RPCs.
func (ClaimsBridgeInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx = bridgeClaims(ctx)
		return next(ctx, req)
	}
}

// WrapStreamingClient is a no-op for client streaming.
func (ClaimsBridgeInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler bridges authn info to auth.Claims context for streaming RPCs.
func (ClaimsBridgeInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx = bridgeClaims(ctx)
		return next(ctx, conn)
	}
}

// bridgeClaims extracts *Claims from authn.GetInfo and adds it to the context.
func bridgeClaims(ctx context.Context) context.Context {
	if info := authn.GetInfo(ctx); info != nil {
		if claims, ok := info.(*Claims); ok {
			return WithClaims(ctx, claims)
		}
	}
	return ctx
}
