package webhook_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/adapter/webhook/mocks"
	"github.com/pannpers/go-logging/logging"
)

func newLogger(t *testing.T) *logging.Logger {
	t.Helper()
	l, err := logging.New()
	require.NoError(t, err)
	return l
}

// tokenWith returns a parsed webhook token carrying the given private claims.
func tokenWith(t *testing.T, privateClaims map[string]any) jwt.Token {
	t.Helper()
	tok := jwt.New()
	for k, v := range privateClaims {
		require.NoError(t, tok.Set(k, v))
	}
	return tok
}

// appendClaims posts body to the handler and decodes the append_claims of a
// 200 response.
func appendClaims(t *testing.T, handler http.Handler, body string) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pre-access-token", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		AppendClaims []map[string]any `json:"append_claims"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp.AppendClaims
}

func TestPreAccessTokenHandler_HumanUser_InjectsEmailClaim(t *testing.T) {
	t.Parallel()
	validator := mocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateWebhookToken(mock.Anything, "signed-body").Return(tokenWith(t, map[string]any{
		"user": map[string]any{"human": map[string]any{"email": "alice@example.com"}},
	}), nil)
	handler := webhook.NewPreAccessTokenHandler(validator, newLogger(t))

	claims := appendClaims(t, handler, "signed-body")

	assert.Equal(t, []map[string]any{{"key": "email", "value": "alice@example.com"}}, claims)
}

func TestPreAccessTokenHandler_MachineUser_OmitsEmailClaim(t *testing.T) {
	t.Parallel()
	validator := mocks.NewMockTokenValidator(t)
	// Machine users have no `user.human` object at all.
	validator.EXPECT().ValidateWebhookToken(mock.Anything, "signed-body").Return(tokenWith(t, map[string]any{
		"user": map[string]any{"id": "312909075212468632"},
	}), nil)
	handler := webhook.NewPreAccessTokenHandler(validator, newLogger(t))

	claims := appendClaims(t, handler, "signed-body")

	assert.Empty(t, claims)
}

func TestPreAccessTokenHandler_InvalidToken_Returns401(t *testing.T) {
	t.Parallel()
	validator := mocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateWebhookToken(mock.Anything, "forged-body").Return(nil, errors.New("bad signature"))
	handler := webhook.NewPreAccessTokenHandler(validator, newLogger(t))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pre-access-token", bytes.NewBufferString("forged-body"))
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPreAccessTokenHandler_EmptyBody_Returns400(t *testing.T) {
	t.Parallel()
	handler := webhook.NewPreAccessTokenHandler(mocks.NewMockTokenValidator(t), newLogger(t))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pre-access-token", bytes.NewBuffer(nil))
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPreAccessTokenHandler_GET_ReturnsMethodNotAllowed(t *testing.T) {
	t.Parallel()
	handler := webhook.NewPreAccessTokenHandler(mocks.NewMockTokenValidator(t), newLogger(t))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pre-access-token", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
}
