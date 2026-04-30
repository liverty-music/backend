package webhook_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/adapter/webhook"
)

const testAutoVerifyAud = "urn:liverty-music:webhook:auto-verify-email"

func TestAutoVerifyEmailHandler_ValidJWT_ReturnsVerifiedTrue(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewAutoVerifyEmailHandler(
		fixture.newValidator(t, testAutoVerifyAud),
		newLogger(t),
	)

	body := fixture.signWebhookJWT(t, testIssuer, testAutoVerifyAud, map[string]any{
		"request": map[string]any{
			"email": map[string]any{
				"address":     "alice@example.com",
				"is_verified": false,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auto-verify-email", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Email struct {
			IsVerified bool `json:"is_verified"`
		} `json:"email"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Email.IsVerified)
}

func TestAutoVerifyEmailHandler_WrongAudience_Returns401(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewAutoVerifyEmailHandler(
		fixture.newValidator(t, testAutoVerifyAud),
		newLogger(t),
	)

	// Re-use the pre-access-token audience — should be rejected.
	body := fixture.signWebhookJWT(t, testIssuer, testPreAccessAud, map[string]any{
		"request": map[string]any{"email": map[string]any{"address": "x@x"}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auto-verify-email", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAutoVerifyEmailHandler_EmptyBody_Returns400(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewAutoVerifyEmailHandler(
		fixture.newValidator(t, testAutoVerifyAud),
		newLogger(t),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auto-verify-email", bytes.NewBuffer(nil))
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAutoVerifyEmailHandler_GET_ReturnsMethodNotAllowed(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewAutoVerifyEmailHandler(
		fixture.newValidator(t, testAutoVerifyAud),
		newLogger(t),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auto-verify-email", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
}
