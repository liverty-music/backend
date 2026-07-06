package webhook_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zitadel/zitadel-go/v3/pkg/actions"

	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/entity"
)

const testLoginEventSigningKey = "test-login-event-signing-key"

// stubUserResolver records GetByExternalID calls and returns a canned result.
type stubUserResolver struct {
	user      *entity.User
	err       error
	calls     int
	lastExtID string
}

func (s *stubUserResolver) GetByExternalID(_ context.Context, externalID string) (*entity.User, error) {
	s.calls++
	s.lastExtID = externalID
	return s.user, s.err
}

// stubPublisher records PublishEvent calls and returns a canned error.
type stubPublisher struct {
	err     error
	calls   int
	subject string
	data    any
}

func (s *stubPublisher) PublishEvent(_ context.Context, subject string, data any) error {
	s.calls++
	s.subject = subject
	s.data = data
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

func TestLoginEventHandler_UserInitiatedLogin_EmitsAccountLoginOnce(t *testing.T) {
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{}
	handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, users, pub, newLogger(t))

	body := loginEventBody(t, "session.user.checked", "zitadel-sub-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newSignedRequest(t, body, testLoginEventSigningKey))

	require.Equal(t, http.StatusOK, rec.Code)
	// The decoded event_payload userID was resolved to the platform UserID...
	assert.Equal(t, 1, users.calls)
	assert.Equal(t, "zitadel-sub-123", users.lastExtID)
	// ...and exactly one ACCOUNT.login was published carrying that UserID.
	require.Equal(t, 1, pub.calls)
	assert.Equal(t, entity.SubjectAccountLogin, pub.subject)
	data, ok := pub.data.(entity.AccountLoginData)
	require.True(t, ok, "published data must be entity.AccountLoginData")
	assert.Equal(t, "platform-uuid-1", data.UserID)
}

func TestLoginEventHandler_InvalidSignature_Rejected_NoPublish(t *testing.T) {
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{}
	handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, users, pub, newLogger(t))

	// Signed with a different key, so the signature does not verify.
	body := loginEventBody(t, "session.user.checked", "zitadel-sub-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newSignedRequest(t, body, "wrong-signing-key"))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, 0, users.calls)
	assert.Equal(t, 0, pub.calls)
}

func TestLoginEventHandler_MissingSignature_Rejected_NoPublish(t *testing.T) {
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{}
	handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, users, pub, newLogger(t))

	body := loginEventBody(t, "session.user.checked", "zitadel-sub-123")
	rec := httptest.NewRecorder()
	// No ZITADEL-Signature header set.
	req := httptest.NewRequest(http.MethodPost, "/account-login-event", bytes.NewReader(body))
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, 0, pub.calls)
}

func TestLoginEventHandler_WrongEventType_Skips_Returns200(t *testing.T) {
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{}
	handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, users, pub, newLogger(t))

	// A different event type (e.g. a mis-bound Target) must not emit a login.
	body := loginEventBody(t, "session.added", "zitadel-sub-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newSignedRequest(t, body, testLoginEventSigningKey))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, users.calls, "no lookup for a non-login event type")
	assert.Equal(t, 0, pub.calls, "no publish for a non-login event type")
}

func TestLoginEventHandler_MissingUserID_Skips_Returns200(t *testing.T) {
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{}
	handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, users, pub, newLogger(t))

	// A session.user.checked whose decoded payload lacks userID: skip, never fail.
	body := loginEventBody(t, "session.user.checked", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newSignedRequest(t, body, testLoginEventSigningKey))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, users.calls, "no lookup when identifier is absent")
	assert.Equal(t, 0, pub.calls, "no publish when identifier is absent")
}

func TestLoginEventHandler_LookupMiss_Skips_Returns200(t *testing.T) {
	users := &stubUserResolver{err: errors.New("user not found")}
	pub := &stubPublisher{}
	handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, users, pub, newLogger(t))

	body := loginEventBody(t, "session.user.checked", "zitadel-sub-unprovisioned")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newSignedRequest(t, body, testLoginEventSigningKey))

	require.Equal(t, http.StatusOK, rec.Code, "a lookup miss must never fail login")
	assert.Equal(t, 1, users.calls)
	assert.Equal(t, 0, pub.calls, "no publish when the sub cannot be resolved")
}

func TestLoginEventHandler_PublishFailure_DoesNotAffectResponse(t *testing.T) {
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{err: errors.New("nats unavailable")}
	handler := webhook.NewLoginEventHandler(testLoginEventSigningKey, users, pub, newLogger(t))

	body := loginEventBody(t, "session.user.checked", "zitadel-sub-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newSignedRequest(t, body, testLoginEventSigningKey))

	// The publish failed but login (the webhook response) still succeeds.
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, pub.calls)
}

func TestLoginEventHandler_GET_ReturnsMethodNotAllowed(t *testing.T) {
	handler := webhook.NewLoginEventHandler(
		testLoginEventSigningKey, &stubUserResolver{}, &stubPublisher{}, newLogger(t),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account-login-event", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
}
