package auth

import (
	"context"
	"net/http"

	"connectrpc.com/authn"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/pkg/authctx"
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

// NewClaimsBridgeInterceptor returns a server interceptor that reads
// authn.GetInfo and injects the *authctx.Claims into the context via
// authctx.WithClaims, where handlers read them with authctx.GetClaims.
//
// It applies to every RPC type (unary and streaming), as the v1
// interceptor did through its WrapUnary and WrapStreamingHandler methods.
func NewClaimsBridgeInterceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return next(bridgeClaims(ctx), spec, stream)
		}
	}
}

// bridgeClaims extracts *Claims from authn.GetInfo and adds it to the context.
func bridgeClaims(ctx context.Context) context.Context {
	if info := authn.GetInfo(ctx); info != nil {
		if claims, ok := info.(*authctx.Claims); ok {
			return authctx.WithClaims(ctx, claims)
		}
	}
	return ctx
}
