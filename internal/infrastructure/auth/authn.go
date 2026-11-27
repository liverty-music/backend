package auth

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/authn"
	connectv1 "connectrpc.com/connect"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/pkg/authctx"
	"github.com/pannpers/go-apperr/apperr"
)

// NewAuthFunc creates an authn.AuthFunc that validates JWT bearer tokens
// using the provided TokenValidator and returns *Claims on success.
//
// A failure keeps its code (see authError).
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
			return nil, authError(err)
		}

		return claims, nil
	}
}

// authError turns a token check's failure into the call's error, keeping
// its code: a token for another app is PermissionDenied, and a token that
// cannot be checked is Unavailable (or Internal for our own configuration),
// so no app signs the person out over an outage. Anything else is
// Unauthenticated. The messages name no detail of the check.
//
// The errors are connect v1 errors: the authn middleware writes them with a
// connect v1 ErrorWriter, which reads only a v1 *connect.Error's code.
func authError(err error) error {
	switch {
	case errors.Is(err, apperr.ErrPermissionDenied):
		return connectv1.NewError(connectv1.CodePermissionDenied, errors.New("the sign-in is not for this app"))
	case errors.Is(err, apperr.ErrUnavailable):
		return connectv1.NewError(connectv1.CodeUnavailable, errors.New("the sign-in cannot be checked now"))
	case errors.Is(err, apperr.ErrInternal):
		return connectv1.NewError(connectv1.CodeInternal, errors.New("the sign-in cannot be checked"))
	default:
		return authn.Errorf("invalid token: %w", err)
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
