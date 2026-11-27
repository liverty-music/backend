package auth

import (
	"context"
	"slices"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/pkg/authctx"
)

// ActiveChecker reports whether the sign-in service holds a token active;
// Introspector implements it.
type ActiveChecker interface {
	Active(ctx context.Context, token string, expiry time.Time) (bool, error)
}

// SignInChecker accepts only an active access token issued for one app. It
// checks, in order:
//
//  1. the signature, issuer and expiry, and that the token names an identity
//     and an email (JWTValidator), so a garbage, foreign or expired token
//     never reaches the sign-in service;
//  2. that `aud` contains the app's project id;
//  3. that the sign-in service reports the token active (introspection),
//     which refuses an ID token, a revoked token and a removed identity's.
//
// The claims keep coming from the verified JWT.
type SignInChecker struct {
	jwt      *JWTValidator
	audience string
	active   ActiveChecker
}

// NewSignInChecker returns a checker of tokens issued for the project
// audience, asking active whether a token is active.
func NewSignInChecker(jwt *JWTValidator, audience string, active ActiveChecker) *SignInChecker {
	return &SignInChecker{jwt: jwt, audience: audience, active: active}
}

// ValidateToken implements TokenValidator.
//
// # Possible errors
//
//   - Unauthenticated: the token is not signed by the sign-in service, has
//     expired, or is not an active access token (an ID token, a revoked
//     token, a removed identity's).
//   - PermissionDenied: the token is not issued for this app's project.
//   - Unavailable: the sign-in service cannot be asked, or did not answer
//     within 5 seconds.
//   - Internal: the sign-in service refused this API's credential.
func (c *SignInChecker) ValidateToken(ctx context.Context, token string) (*authctx.Claims, error) {
	claims, expiry, err := c.jwt.validate(ctx, token)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(claims.Audiences, c.audience) {
		return nil, apperr.New(codes.PermissionDenied, "the token is not issued for this app")
	}
	active, err := c.active.Active(ctx, token, expiry)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, apperr.New(codes.Unauthenticated, "the token is not an active access token")
	}
	return claims, nil
}

// unconfiguredChecker refuses every token: this deployment does not serve
// the API it guards, so it holds no key to check its tokens with.
type unconfiguredChecker struct {
	api string
}

// NewUnconfiguredChecker returns a TokenValidator for a server whose API
// this deployment does not serve (it has no key for it). Every signed-in
// call fails with Internal; calls open to everyone still work.
func NewUnconfiguredChecker(api string) TokenValidator {
	return unconfiguredChecker{api: api}
}

// ValidateToken implements TokenValidator.
func (u unconfiguredChecker) ValidateToken(context.Context, string) (*authctx.Claims, error) {
	return nil, apperr.New(codes.Internal, "this deployment does not check sign-ins of the "+u.api)
}
