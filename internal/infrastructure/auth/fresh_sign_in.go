package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// FreshSignInMaxAge is how long ago a sign-in may have happened for its ID
// token to prove a fresh sign-in.
const FreshSignInMaxAge = 5 * time.Minute

// FreshSignInVerifier checks the proof that a caller signed in within the last
// FreshSignInMaxAge: the ID token the sign-in service issued to the fan app
// for that sign-in. It shares the JWKS cache and accepted issuers of the
// access-token validator it is derived from.
type FreshSignInVerifier struct {
	validator *JWTValidator
	clientID  string
	now       func() time.Time
}

// NewFreshSignInVerifier returns a verifier that accepts ID tokens issued to
// the app clientID, signed with the receiver's JWKS by one of its accepted
// issuers. now is the clock (time.Now in production).
func (v *JWTValidator) NewFreshSignInVerifier(clientID string, now func() time.Time) *FreshSignInVerifier {
	return &FreshSignInVerifier{validator: v, clientID: clientID, now: now}
}

// VerifyFreshSignIn checks that idToken is validly signed by an accepted
// issuer, unexpired, issued to the fan app, names the identity
// providerUserID, and shows a sign-in at most FreshSignInMaxAge before now.
// The token is never logged.
//
// # Possible errors
//
//   - PermissionDenied: any of the checks fails.
//   - Unavailable: the sign-in service's keys cannot be fetched.
func (f *FreshSignInVerifier) VerifyFreshSignIn(ctx context.Context, idToken, providerUserID string) error {
	keySet, err := f.validator.jwks.keySet(ctx)
	if err != nil {
		return apperr.Wrap(err, codes.Unavailable, "failed to get the sign-in service's keys")
	}
	token, err := jwt.Parse([]byte(idToken), jwt.WithKeySet(keySet), jwt.WithValidate(true))
	if err != nil {
		return apperr.Wrap(err, codes.PermissionDenied, "the sign-in proof is not a valid token")
	}

	issuer, _ := token.Issuer()
	if !slices.Contains(f.validator.acceptedIssuers, issuer) {
		return apperr.New(codes.PermissionDenied, "the sign-in proof is from another issuer", slog.String("issuer", issuer))
	}
	if !slices.Contains(extractAudiences(token), f.clientID) {
		return apperr.New(codes.PermissionDenied, "the sign-in proof was issued to another app")
	}
	if sub, _ := token.Subject(); sub != providerUserID {
		return apperr.New(codes.PermissionDenied, "the sign-in proof names another identity")
	}
	authTime, ok := numericDate(token, "auth_time")
	if !ok {
		return apperr.New(codes.PermissionDenied, "the sign-in proof carries no sign-in time")
	}
	if age := f.now().Sub(authTime); age > FreshSignInMaxAge {
		return apperr.New(codes.PermissionDenied, "the sign-in is not fresh", slog.Duration("age", age))
	}
	return nil
}

// numericDate reads a NumericDate claim (seconds since the epoch) that the
// token library does not parse itself.
func numericDate(token jwt.Token, name string) (time.Time, bool) {
	v, ok := token.Field(name)
	if !ok {
		return time.Time{}, false
	}
	var secs float64
	switch n := v.(type) {
	case float64:
		secs = n
	case int64:
		secs = float64(n)
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return time.Time{}, false
		}
		secs = f
	default:
		return time.Time{}, false
	}
	return time.Unix(int64(secs), 0), true
}
