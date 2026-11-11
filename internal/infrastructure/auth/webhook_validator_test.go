package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/auth"
)

const (
	testWebhookIssuer = "https://auth.test.liverty-music.app"
	testPreAccessAud  = "urn:liverty-music:webhook:pre-access-token"
)

// jwksFixture wires up an in-memory RSA key + a JWKS endpoint backed by
// httptest so a jwk.Cache (and therefore a JWTValidator + WebhookValidator)
// can refresh against it.
type jwksFixture struct {
	server     *httptest.Server
	privateKey *rsa.PrivateKey
	jwks       jwk.Set
}

func newJWKSFixture(t *testing.T) *jwksFixture {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	public, err := jwk.Import[jwk.Key](privateKey.PublicKey)
	require.NoError(t, err)
	require.NoError(t, public.Set(jwk.KeyIDKey, "test-key-id"))
	require.NoError(t, public.Set(jwk.AlgorithmKey, jwa.RS256()))

	set := jwk.NewSet()
	require.NoError(t, set.AddKey(public))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(server.Close)

	return &jwksFixture{server: server, privateKey: privateKey, jwks: set}
}

// newValidator returns a WebhookValidator that expects the given `aud` and
// trusts the fixture's JWKS endpoint via a freshly constructed JWTValidator.
func (f *jwksFixture) newValidator(t *testing.T, expectedAud string) *auth.WebhookValidator {
	t.Helper()
	v, err := auth.NewJWTValidator(testWebhookIssuer, f.server.URL, 15*time.Minute)
	require.NoError(t, err)
	return v.NewWebhookValidator(expectedAud)
}

// signWebhookJWT builds a signed webhook JWT with the given issuer, audience,
// and arbitrary private claims. Useful for building handler inputs.
func (f *jwksFixture) signWebhookJWT(
	t *testing.T,
	issuer, audience string,
	privateClaims map[string]any,
) string {
	t.Helper()
	tok := jwt.New()
	require.NoError(t, tok.Set(jwt.IssuerKey, issuer))
	require.NoError(t, tok.Set(jwt.AudienceKey, []string{audience}))
	require.NoError(t, tok.Set(jwt.IssuedAtKey, time.Now()))
	require.NoError(t, tok.Set(jwt.ExpirationKey, time.Now().Add(1*time.Minute)))
	for k, v := range privateClaims {
		require.NoError(t, tok.Set(k, v))
	}
	// Set `kid` on the protected headers so the JWKS-based validator can
	// look up the matching public key from the set.
	protected := jws.NewHeaders()
	require.NoError(t, protected.Set(jws.KeyIDKey, "test-key-id"))
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), f.privateKey, jws.WithProtectedHeaders(protected)))
	require.NoError(t, err)
	return string(signed)
}

func TestWebhookValidator_ValidateWebhookToken(t *testing.T) {
	t.Parallel()

	t.Run("accepts a token signed by the JWKS and returns its claims", func(t *testing.T) {
		t.Parallel()
		fixture := newJWKSFixture(t)
		body := fixture.signWebhookJWT(t, testWebhookIssuer, testPreAccessAud, map[string]any{
			"user": map[string]any{"human": map[string]any{"email": "alice@example.com"}},
		})

		token, err := fixture.newValidator(t, testPreAccessAud).ValidateWebhookToken(context.Background(), body)

		require.NoError(t, err)
		user, ok := token.Field("user")
		require.True(t, ok)
		assert.Equal(t, map[string]any{"human": map[string]any{"email": "alice@example.com"}}, user)
	})

	t.Run("rejects a token signed by another key", func(t *testing.T) {
		t.Parallel()
		trusted := newJWKSFixture(t)
		other := newJWKSFixture(t)
		body := other.signWebhookJWT(t, testWebhookIssuer, testPreAccessAud, nil)

		_, err := trusted.newValidator(t, testPreAccessAud).ValidateWebhookToken(context.Background(), body)

		assert.Error(t, err)
	})

	t.Run("rejects a body that is not a JWT", func(t *testing.T) {
		t.Parallel()
		fixture := newJWKSFixture(t)

		_, err := fixture.newValidator(t, testPreAccessAud).ValidateWebhookToken(context.Background(), "not-a-jwt")

		assert.Error(t, err)
	})
}
