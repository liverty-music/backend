package webhook_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zitadel/zitadel-go/v3/pkg/actions"

	"github.com/liverty-music/backend/internal/adapter/webhook"
)

const testLoginEventSigningKey = "test-login-event-signing-key"

// stubLoginRecorder records RecordLogin calls and returns a canned error.
type stubLoginRecorder struct {
	err            error
	calls          int
	providerUserID string
	at             time.Time
}

func (s *stubLoginRecorder) RecordLogin(_ context.Context, providerUserID string, at time.Time) error {
	s.calls++
	s.providerUserID = providerUserID
	s.at = at
	return s.err
}

// loginEventBody builds a JSON login-event webhook body modelling a Zitadel
// Actions v2 event execution: an `event_type` plus a base64-encoded
// `event_payload`. For session.user.checked the login user lives in the decoded
// payload's `userID` (the top-level userID would be the Login-UI editor). When
// userID is empty the field is omitted so the "missing identifier" path can be
// exercised.
func loginEventBody(t *testing.T, eventType, userID string) []byte {
	t.Helper()
	inner := map[string]any{}
	if userID != "" {
		inner["userID"] = userID
	}
	raw, err := json.Marshal(inner)
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"event_type": eventType,
		// Top-level userID is the editor (Login-UI service user); the handler
		// must ignore it and read the decoded event_payload instead.
		"userID":        "login-ui-editor",
		"created_at":    "2026-10-22T03:04:05.123456Z",
		"event_payload": base64.StdEncoding.EncodeToString(raw),
	})
	require.NoError(t, err)
	return body
}

// newSignedRequest builds a POST request with the ZITADEL-Signature header
// computed from the body and the given signing key.
func newSignedRequest(t *testing.T, body []byte, signingKey string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/account-login-event", bytes.NewReader(body))
	req.Header.Set(actions.SigningHeader, actions.ComputeSignatureHeader(time.Now(), body, signingKey))
	return req
}

func TestLoginEventHandler(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/webhook/login-events "Valid signature"
	// @spec components/adapter/fan/api/webhook/login-events "Fan signs in"
	t.Run("records the login of the identity at the notice's time", func(t *testing.T) {
		t.Parallel()
		logins := &stubLoginRecorder{}
		handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, logins, newLogger(t))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newSignedRequest(t, loginEventBody(t, "session.user.checked", "zitadel-sub-123"), testLoginEventSigningKey))

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, 1, logins.calls)
		// The decoded event_payload userID, not the top-level editor.
		assert.Equal(t, "zitadel-sub-123", logins.providerUserID)
		assert.Equal(t, time.Date(2026, 10, 22, 3, 4, 5, 123456000, time.UTC), logins.at.UTC())
	})

	// @spec components/adapter/fan/api/webhook/login-events "Forged notice"
	t.Run("rejects a notice with a missing or invalid signature with 401 and records nothing", func(t *testing.T) {
		t.Parallel()
		logins := &stubLoginRecorder{}
		handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, logins, newLogger(t))
		body := loginEventBody(t, "session.user.checked", "zitadel-sub-123")

		forged := httptest.NewRecorder()
		handler.ServeHTTP(forged, newSignedRequest(t, body, "wrong-signing-key"))
		unsigned := httptest.NewRecorder()
		handler.ServeHTTP(unsigned, httptest.NewRequest(http.MethodPost, "/account-login-event", bytes.NewReader(body)))

		assert.Equal(t, http.StatusUnauthorized, forged.Code)
		assert.Equal(t, http.StatusUnauthorized, unsigned.Code)
		assert.Equal(t, 0, logins.calls)
	})

	// @spec components/adapter/fan/api/webhook/login-events "Key not configured"
	t.Run("answers 503 and records nothing without a signing key", func(t *testing.T) {
		t.Parallel()
		logins := &stubLoginRecorder{}
		handler := webhook.NewLoginEventHandler("", logins, newLogger(t))

		// actions.ValidateRequestPayload would accept this body: its HMAC is
		// computed with the same empty key.
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newSignedRequest(t, loginEventBody(t, "session.user.checked", "zitadel-sub-123"), ""))

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, 0, logins.calls)
	})

	// @spec components/adapter/fan/api/webhook/login-events "Notice of another kind"
	t.Run("answers 200 and records nothing for a notice of another kind", func(t *testing.T) {
		t.Parallel()
		logins := &stubLoginRecorder{}
		handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, logins, newLogger(t))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newSignedRequest(t, loginEventBody(t, "session.added", "zitadel-sub-123"), testLoginEventSigningKey))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 0, logins.calls)
	})

	t.Run("answers 200 and records nothing for a notice that names no identity", func(t *testing.T) {
		t.Parallel()
		logins := &stubLoginRecorder{}
		handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, logins, newLogger(t))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newSignedRequest(t, loginEventBody(t, "session.user.checked", ""), testLoginEventSigningKey))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 0, logins.calls)
	})

	// The response of an event execution is ignored by Zitadel, so the 500
	// never changes the sign-in.
	// @spec components/adapter/fan/api/webhook/login-events "Recording fails"
	t.Run("answers 500 when the login cannot be recorded", func(t *testing.T) {
		t.Parallel()
		logins := &stubLoginRecorder{err: apperr.New(codes.Unavailable, "database down")}
		handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, logins, newLogger(t))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newSignedRequest(t, loginEventBody(t, "session.user.checked", "zitadel-sub-123"), testLoginEventSigningKey))

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, 1, logins.calls)
	})

	t.Run("answers 405 to a GET", func(t *testing.T) {
		t.Parallel()
		handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, &stubLoginRecorder{}, newLogger(t))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/account-login-event", nil))

		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
	})
}
