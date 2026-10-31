package webhook_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zitadel/zitadel-go/v3/pkg/actions"

	"github.com/liverty-music/backend/internal/adapter/webhook"
)

const testPasswordSetupRefusalSigningKey = "test-password-setup-refusal-signing-key"

// passwordSetupRequestBody builds a Zitadel Actions v2 request-execution body
// for a fan setting their own password through method.
func passwordSetupRequestBody(t *testing.T, method string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"fullMethod": method,
		"instanceID": "instance-1",
		"orgID":      "product-org-1",
		"projectID":  "",
		"userID":     "zitadel-fan-1",
		"request":    map[string]any{"userId": "zitadel-fan-1", "newPassword": map[string]any{"password": "N3w-password!"}},
	})
	require.NoError(t, err)
	return body
}

func signedPasswordSetupRequest(t *testing.T, body []byte, signingKey string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/refuse-password-setup", bytes.NewReader(body))
	req.Header.Set(actions.SigningHeader, actions.ComputeSignatureHeader(time.Now(), body, signingKey))
	return req
}

func TestPasswordSetupRefusalHandler(t *testing.T) {
	t.Parallel()

	t.Run("refuses every signed password-setting request with 403", func(t *testing.T) {
		t.Parallel()
		handler := webhook.NewPasswordSetupRefusalHandler(testPasswordSetupRefusalSigningKey, newLogger(t))

		for _, method := range []string{
			"/zitadel.user.v2.UserService/SetPassword",
			"/zitadel.user.v2.UserService/PasswordReset",
			"/zitadel.auth.v1.AuthService/UpdateMyPassword",
		} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, signedPasswordSetupRequest(t, passwordSetupRequestBody(t, method), testPasswordSetupRefusalSigningKey))

			assert.Equal(t, http.StatusForbidden, rec.Code, method)
			assert.NotContains(t, rec.Body.String(), "N3w-password!", method)
		}
	})

	t.Run("refuses an unparsable signed body with 403", func(t *testing.T) {
		t.Parallel()
		handler := webhook.NewPasswordSetupRefusalHandler(testPasswordSetupRefusalSigningKey, newLogger(t))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, signedPasswordSetupRequest(t, []byte("not json"), testPasswordSetupRefusalSigningKey))

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("rejects a request with a missing or invalid signature with 401", func(t *testing.T) {
		t.Parallel()
		handler := webhook.NewPasswordSetupRefusalHandler(testPasswordSetupRefusalSigningKey, newLogger(t))
		body := passwordSetupRequestBody(t, "/zitadel.user.v2.UserService/SetPassword")

		forged := httptest.NewRecorder()
		handler.ServeHTTP(forged, signedPasswordSetupRequest(t, body, "wrong-signing-key"))
		unsigned := httptest.NewRecorder()
		handler.ServeHTTP(unsigned, httptest.NewRequest(http.MethodPost, "/refuse-password-setup", bytes.NewReader(body)))

		assert.Equal(t, http.StatusUnauthorized, forged.Code)
		assert.Equal(t, http.StatusUnauthorized, unsigned.Code)
	})

	t.Run("answers 503 when the signing key is not configured, which still refuses", func(t *testing.T) {
		t.Parallel()
		handler := webhook.NewPasswordSetupRefusalHandler("", newLogger(t))
		body := passwordSetupRequestBody(t, "/zitadel.user.v2.UserService/SetPassword")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, signedPasswordSetupRequest(t, body, ""))

		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("rejects a method other than POST with 405", func(t *testing.T) {
		t.Parallel()
		handler := webhook.NewPasswordSetupRefusalHandler(testPasswordSetupRefusalSigningKey, newLogger(t))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/refuse-password-setup", nil))

		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
}
