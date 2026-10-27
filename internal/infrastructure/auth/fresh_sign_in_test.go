package auth_test

import (
	"context"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/auth"
)

const (
	freshSignInIssuer   = "https://auth.example.test"
	freshSignInClientID = "fan-client"
	freshSignInSubject  = "zitadel-fan-1"
)

// idTokenClaims are the claims of a test ID token; a zero authTime omits
// auth_time.
type idTokenClaims struct {
	issuer   string
	audience string
	subject  string
	authTime time.Time
}

// freshIDTokenClaims are the claims of an ID token for the fan app's sign-in
// of freshSignInSubject at authTime.
func freshIDTokenClaims(authTime time.Time) idTokenClaims {
	return idTokenClaims{issuer: freshSignInIssuer, audience: freshSignInClientID, subject: freshSignInSubject, authTime: authTime}
}

// createIDToken signs an ID token with the claims, valid for an hour.
func createIDToken(t *testing.T, privateKey *rsa.PrivateKey, c idTokenClaims) string {
	t.Helper()
	token := jwt.New()
	require.NoError(t, token.Set(jwt.IssuerKey, c.issuer))
	require.NoError(t, token.Set(jwt.AudienceKey, []string{c.audience}))
	require.NoError(t, token.Set(jwt.SubjectKey, c.subject))
	require.NoError(t, token.Set(jwt.IssuedAtKey, time.Now()))
	require.NoError(t, token.Set(jwt.ExpirationKey, time.Now().Add(time.Hour)))
	if !c.authTime.IsZero() {
		require.NoError(t, token.Set("auth_time", c.authTime.Unix()))
	}
	key, err := jwk.Import[jwk.Key](privateKey)
	require.NoError(t, err)
	require.NoError(t, key.Set(jwk.KeyIDKey, "test-key-id"))
	signed, err := jwt.Sign(token, jwt.WithKey(jwa.RS256(), key))
	require.NoError(t, err)
	return string(signed)
}

func TestFreshSignInVerifier_VerifyFreshSignIn(t *testing.T) {
	t.Parallel()
	server, privateKey, _ := setupTestJWKS(t)
	t.Cleanup(server.Close)
	validator, err := auth.NewJWTValidator(freshSignInIssuer, server.URL, time.Hour)
	require.NoError(t, err)
	t.Cleanup(func() { _ = validator.Close() })
	now := time.Now()
	verifier := validator.NewFreshSignInVerifier(freshSignInClientID, func() time.Time { return now })
	ctx := context.Background()

	// @spec components/adapter/fan/api/rpc/user "Fresh sign-in"
	t.Run("accept the fan app's ID token of the caller's sign-in 2 minutes ago", func(t *testing.T) {
		t.Parallel()
		token := createIDToken(t, privateKey, freshIDTokenClaims(now.Add(-2*time.Minute)))

		assert.NoError(t, verifier.VerifyFreshSignIn(ctx, token, freshSignInSubject))
	})

	otherKey := newSigningKey(t)
	rejected := []struct {
		name  string
		token func(t *testing.T) string
	}{
		{
			// @spec components/adapter/fan/api/rpc/user "Old sign-in"
			name: "a sign-in 3 days ago",
			token: func(t *testing.T) string {
				return createIDToken(t, privateKey, freshIDTokenClaims(now.Add(-72*time.Hour)))
			},
		},
		{
			name: "a sign-in just over 5 minutes ago",
			token: func(t *testing.T) string {
				return createIDToken(t, privateKey, freshIDTokenClaims(now.Add(-5*time.Minute-time.Second)))
			},
		},
		{
			// @spec components/adapter/fan/api/rpc/user "Proof of another identity"
			name: "a fresh sign-in of another identity",
			token: func(t *testing.T) string {
				c := freshIDTokenClaims(now.Add(-time.Minute))
				c.subject = "zitadel-fan-2"
				return createIDToken(t, privateKey, c)
			},
		},
		{
			name: "a token issued to another app",
			token: func(t *testing.T) string {
				c := freshIDTokenClaims(now.Add(-time.Minute))
				c.audience = "organizer-console"
				return createIDToken(t, privateKey, c)
			},
		},
		{
			name: "a token of another issuer",
			token: func(t *testing.T) string {
				c := freshIDTokenClaims(now.Add(-time.Minute))
				c.issuer = "https://evil.example.test"
				return createIDToken(t, privateKey, c)
			},
		},
		{
			name: "a token without a sign-in time",
			token: func(t *testing.T) string {
				return createIDToken(t, privateKey, freshIDTokenClaims(time.Time{}))
			},
		},
		{
			name: "a token signed with another key",
			token: func(t *testing.T) string {
				return createIDToken(t, otherKey, freshIDTokenClaims(now.Add(-time.Minute)))
			},
		},
		{
			name:  "a value that is not a token",
			token: func(*testing.T) string { return "not-a-token" },
		},
	}
	for _, tc := range rejected {
		t.Run("reject "+tc.name+" with PermissionDenied", func(t *testing.T) {
			t.Parallel()

			err := verifier.VerifyFreshSignIn(ctx, tc.token(t), freshSignInSubject)

			assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		})
	}
}
