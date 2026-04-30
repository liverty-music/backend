package webhook_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/pannpers/go-logging/logging"
)

const (
	testIssuer       = "https://auth.test.liverty-music.app"
	testPreAccessAud = "urn:liverty-music:webhook:pre-access-token"
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
	public, err := jwk.FromRaw(privateKey.PublicKey)
	require.NoError(t, err)
	require.NoError(t, public.Set(jwk.KeyIDKey, "test-key-id"))
	require.NoError(t, public.Set(jwk.AlgorithmKey, jwa.RS256))

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
	v, err := auth.NewJWTValidator(testIssuer, f.server.URL, 15*time.Minute)
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
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, f.privateKey, jws.WithProtectedHeaders(protected)))
	require.NoError(t, err)
	return string(signed)
}

func newLogger(t *testing.T) *logging.Logger {
	t.Helper()
	l, err := logging.New()
	require.NoError(t, err)
	return l
}

func TestPreAccessTokenHandler_HumanUser_InjectsEmailClaim(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewPreAccessTokenHandler(
		fixture.newValidator(t, testPreAccessAud),
		newLogger(t),
	)

	body := fixture.signWebhookJWT(t, testIssuer, testPreAccessAud, map[string]any{
		"user": map[string]any{
			"human": map[string]any{
				"email": "alice@example.com",
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pre-access-token", bytes.NewBufferString(body)).
		WithContext(context.Background())
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		AppendClaims []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"append_claims"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.AppendClaims, 1)
	assert.Equal(t, "email", resp.AppendClaims[0].Key)
	assert.Equal(t, "alice@example.com", resp.AppendClaims[0].Value)
}

func TestPreAccessTokenHandler_MachineUser_OmitsEmailClaim(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewPreAccessTokenHandler(
		fixture.newValidator(t, testPreAccessAud),
		newLogger(t),
	)

	// Machine users have no `user.human` object at all.
	body := fixture.signWebhookJWT(t, testIssuer, testPreAccessAud, map[string]any{
		"user": map[string]any{
			"id": "312909075212468632",
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pre-access-token", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		AppendClaims []any `json:"append_claims"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp.AppendClaims)
}

func TestPreAccessTokenHandler_WrongAudience_Returns401(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewPreAccessTokenHandler(
		fixture.newValidator(t, testPreAccessAud),
		newLogger(t),
	)

	// JWT claims a different webhook's audience — exactly the replay case
	// the `aud` pin is meant to reject.
	body := fixture.signWebhookJWT(t, testIssuer, "urn:liverty-music:webhook:auto-verify-email", map[string]any{
		"user": map[string]any{
			"human": map[string]any{"email": "mallory@example.com"},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pre-access-token", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPreAccessTokenHandler_WrongIssuer_Returns401(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewPreAccessTokenHandler(
		fixture.newValidator(t, testPreAccessAud),
		newLogger(t),
	)

	body := fixture.signWebhookJWT(t, "https://evil.example.com", testPreAccessAud, map[string]any{
		"user": map[string]any{"human": map[string]any{"email": "x@x"}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pre-access-token", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPreAccessTokenHandler_EmptyBody_Returns400(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewPreAccessTokenHandler(
		fixture.newValidator(t, testPreAccessAud),
		newLogger(t),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pre-access-token", bytes.NewBuffer(nil))
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPreAccessTokenHandler_GET_ReturnsMethodNotAllowed(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewPreAccessTokenHandler(
		fixture.newValidator(t, testPreAccessAud),
		newLogger(t),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pre-access-token", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
}
