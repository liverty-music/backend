package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/auth"
)

// setupTestJWKS creates a test JWKS server and returns the server, private key, and public key set.
func setupTestJWKS(t *testing.T) (*httptest.Server, *rsa.PrivateKey, jwk.Set) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "failed to generate RSA key")

	publicKey, err := jwk.FromRaw(privateKey.PublicKey)
	require.NoError(t, err, "failed to create JWK from public key")

	require.NoError(t, publicKey.Set(jwk.KeyIDKey, "test-key-id"), "failed to set key ID")
	require.NoError(t, publicKey.Set(jwk.AlgorithmKey, jwa.RS256), "failed to set algorithm")

	keySet := jwk.NewSet()
	require.NoError(t, keySet.AddKey(publicKey), "failed to add key to set")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(keySet)
	}))

	return server, privateKey, keySet
}

// testTokenOptions holds optional claims for createTestToken.
type testTokenOptions struct {
	// emailVerified, when non-nil, sets the email_verified private claim.
	// When nil the claim is omitted entirely (tests the missing-claim case).
	emailVerified *bool
}

// withEmailVerified returns a testTokenOptions with EmailVerified set to v.
func withEmailVerified(v bool) testTokenOptions {
	return testTokenOptions{emailVerified: &v}
}

// createTestToken creates a signed JWT token for testing.
func createTestToken(t *testing.T, privateKey *rsa.PrivateKey, issuer, subject, email, name string, expiry time.Duration, opts ...testTokenOptions) string {
	t.Helper()

	token := jwt.New()
	require.NoError(t, token.Set(jwt.IssuerKey, issuer), "failed to set issuer")
	require.NoError(t, token.Set(jwt.SubjectKey, subject), "failed to set subject")
	require.NoError(t, token.Set("email", email), "failed to set email")

	if name != "" {
		require.NoError(t, token.Set("name", name), "failed to set name")
	}

	for _, opt := range opts {
		if opt.emailVerified != nil {
			require.NoError(t, token.Set("email_verified", *opt.emailVerified), "failed to set email_verified")
		}
	}

	require.NoError(t, token.Set(jwt.IssuedAtKey, time.Now()), "failed to set issued at")
	require.NoError(t, token.Set(jwt.ExpirationKey, time.Now().Add(expiry)), "failed to set expiration")

	key, err := jwk.FromRaw(privateKey)
	require.NoError(t, err, "failed to create JWK")
	require.NoError(t, key.Set(jwk.KeyIDKey, "test-key-id"), "failed to set key ID")

	signedToken, err := jwt.Sign(token, jwt.WithKey(jwa.RS256, key))
	require.NoError(t, err, "failed to sign token")

	return string(signedToken)
}

// flakyJWKS serves keySet, or 503 while down, and counts requests.
type flakyJWKS struct {
	down     atomic.Bool
	requests atomic.Int32
	keySet   jwk.Set
}

func (f *flakyJWKS) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.requests.Add(1)
	if f.down.Load() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(f.keySet)
}

// newKeySet returns a private key and the JWKS publishing its public half.
func newKeySet(t *testing.T) (*rsa.PrivateKey, jwk.Set) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	publicKey, err := jwk.FromRaw(privateKey.PublicKey)
	require.NoError(t, err)
	require.NoError(t, publicKey.Set(jwk.KeyIDKey, "test-key-id"))
	require.NoError(t, publicKey.Set(jwk.AlgorithmKey, jwa.RS256))
	keySet := jwk.NewSet()
	require.NoError(t, keySet.AddKey(publicKey))
	return privateKey, keySet
}

// @spec components/infrastructure/backend/process/startup-dependencies "Start while the sign-in service is down"
func TestNewJWTValidator_SignInServiceDown(t *testing.T) {
	t.Parallel()
	_, keySet := newKeySet(t)
	jwks := &flakyJWKS{keySet: keySet}
	jwks.down.Store(true)
	server := httptest.NewServer(jwks)
	t.Cleanup(server.Close)

	validator, err := auth.NewJWTValidator(server.URL, server.URL+"/oauth/v2/keys", 15*time.Minute)

	require.NoError(t, err)
	assert.Equal(t, server.URL, auth.JWTValidatorIssuer(validator))
	assert.Zero(t, jwks.requests.Load(), "building the validator must make no request")
}

// @spec components/infrastructure/backend/process/startup-dependencies "Recovery without restart"
func TestValidateToken_RecoversWhenSignInServiceReturns(t *testing.T) {
	t.Parallel()
	privateKey, keySet := newKeySet(t)
	jwks := &flakyJWKS{keySet: keySet}
	jwks.down.Store(true)
	server := httptest.NewServer(jwks)
	t.Cleanup(server.Close)
	validator, err := auth.NewJWTValidator(server.URL, server.URL+"/oauth/v2/keys", 15*time.Minute)
	require.NoError(t, err)
	token := createTestToken(t, privateKey, server.URL, "user-1", "fan@example.com", "", time.Hour)

	_, err = validator.ValidateToken(context.Background(), token)
	require.Error(t, err, "the keys cannot be fetched while the sign-in service is down")
	_, err = validator.ValidateToken(context.Background(), token)
	require.Error(t, err)

	jwks.down.Store(false)
	claims, err := validator.ValidateToken(context.Background(), token)
	require.NoError(t, err, "the first validation after recovery fetches the keys")
	assert.Equal(t, "user-1", claims.Sub)

	requests := jwks.requests.Load()
	_, err = validator.ValidateToken(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, requests, jwks.requests.Load(), "fetched keys are served from the cache")
}

func TestValidateToken(t *testing.T) {
	t.Parallel()

	type want struct {
		sub           string
		email         string
		name          string
		emailVerified bool
	}
	tests := []struct {
		name    string
		setup   func(t *testing.T) (tokenString string, validator *auth.JWTValidator)
		want    want
		wantErr error
	}{
		{
			name: "return claims when token is valid with all fields",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				tok := createTestToken(t, privateKey, server.URL, "user-123", "test@example.com", "Test User", time.Hour)
				return tok, v
			},
			want: want{
				sub:           "user-123",
				email:         "test@example.com",
				name:          "Test User",
				emailVerified: false,
			},
		},
		{
			name: "return claims without name when name claim is absent",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				tok := createTestToken(t, privateKey, server.URL, "user-456", "another@example.com", "", time.Hour)
				return tok, v
			},
			want: want{
				sub:   "user-456",
				email: "another@example.com",
				name:  "",
			},
		},
		{
			name: "return claims with EmailVerified true when email_verified claim is true",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				tok := createTestToken(t, privateKey, server.URL, "user-123", "verified@example.com", "Verified User", time.Hour, withEmailVerified(true))
				return tok, v
			},
			want: want{
				sub:           "user-123",
				email:         "verified@example.com",
				name:          "Verified User",
				emailVerified: true,
			},
		},
		{
			name: "return claims with EmailVerified false when email_verified claim is false",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				tok := createTestToken(t, privateKey, server.URL, "user-123", "unverified@example.com", "Unverified User", time.Hour, withEmailVerified(false))
				return tok, v
			},
			want: want{
				sub:           "user-123",
				email:         "unverified@example.com",
				name:          "Unverified User",
				emailVerified: false,
			},
		},
		{
			name: "return claims with EmailVerified false when email_verified claim is absent",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				// No withEmailVerified option — claim is absent.
				tok := createTestToken(t, privateKey, server.URL, "user-123", "noverify@example.com", "No Verify User", time.Hour)
				return tok, v
			},
			want: want{
				sub:           "user-123",
				email:         "noverify@example.com",
				name:          "No Verify User",
				emailVerified: false,
			},
		},
		{
			name: "return error when token is expired",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				tok := createTestToken(t, privateKey, server.URL, "user-123", "test@example.com", "Test User", -time.Hour)
				return tok, v
			},
			wantErr: errors.New("expired token"),
		},
		{
			name: "return error when token issuer does not match",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				tok := createTestToken(t, privateKey, "https://wrong-issuer.com", "user-123", "test@example.com", "Test User", time.Hour)
				return tok, v
			},
			wantErr: errors.New("wrong issuer"),
		},
		{
			name: "return error when token is malformed",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, _, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				return "invalid.token.here", v
			},
			wantErr: errors.New("malformed token"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tokenString, validator := tt.setup(t)

			claims, err := validator.ValidateToken(context.Background(), tokenString)

			if tt.wantErr != nil {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.want.sub, claims.Sub)
			assert.Equal(t, tt.want.email, claims.Email)
			assert.Equal(t, tt.want.name, claims.Name)
			assert.Equal(t, tt.want.emailVerified, claims.EmailVerified)
		})
	}
}
