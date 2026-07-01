package webhook_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/entity"
)

const testCreateSessionAud = "urn:liverty-music:webhook:create-session"

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

// createSessionBody builds a signed CreateSession webhook JWT whose payload
// carries request.checks.user.userId = sub. When sub is empty the userId field
// is omitted so the "missing identifier" path can be exercised.
func createSessionBody(t *testing.T, f *jwksFixture, sub string) string {
	t.Helper()
	user := map[string]any{}
	if sub != "" {
		user["userId"] = sub
	}
	return f.signWebhookJWT(t, testIssuer, testCreateSessionAud, map[string]any{
		"request": map[string]any{
			"checks": map[string]any{
				"user": user,
			},
		},
	})
}

func TestCreateSessionHandler_UserInitiatedLogin_EmitsAccountLoginOnce(t *testing.T) {
	fixture := newJWKSFixture(t)
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{}
	handler := webhook.NewCreateSessionHandler(
		fixture.newValidator(t, testCreateSessionAud), users, pub, newLogger(t),
	)

	body := createSessionBody(t, fixture, "zitadel-sub-123")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/create-session", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// sub was resolved to the platform UserID via the lookup...
	assert.Equal(t, 1, users.calls)
	assert.Equal(t, "zitadel-sub-123", users.lastExtID)
	// ...and exactly one ACCOUNT.login was published carrying that UserID.
	require.Equal(t, 1, pub.calls)
	assert.Equal(t, entity.SubjectAccountLogin, pub.subject)
	data, ok := pub.data.(entity.AccountLoginData)
	require.True(t, ok, "published data must be entity.AccountLoginData")
	assert.Equal(t, "platform-uuid-1", data.UserID)
}

func TestCreateSessionHandler_InvalidSignature_Rejected_NoPublish(t *testing.T) {
	fixture := newJWKSFixture(t)
	// A different fixture signs the token, so its signature does not verify
	// against the handler's trusted JWKS.
	forger := newJWKSFixture(t)
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{}
	handler := webhook.NewCreateSessionHandler(
		fixture.newValidator(t, testCreateSessionAud), users, pub, newLogger(t),
	)

	body := createSessionBody(t, forger, "zitadel-sub-123")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/create-session", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, 0, users.calls)
	assert.Equal(t, 0, pub.calls)
}

func TestCreateSessionHandler_MissingUserID_Skips_Returns200(t *testing.T) {
	fixture := newJWKSFixture(t)
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{}
	handler := webhook.NewCreateSessionHandler(
		fixture.newValidator(t, testCreateSessionAud), users, pub, newLogger(t),
	)

	// Login flow that attaches the user via loginName / a later SetSession:
	// request.checks.user.userId is absent.
	body := createSessionBody(t, fixture, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/create-session", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, users.calls, "no lookup when identifier is absent")
	assert.Equal(t, 0, pub.calls, "no publish when identifier is absent")
}

func TestCreateSessionHandler_LookupMiss_Skips_Returns200(t *testing.T) {
	fixture := newJWKSFixture(t)
	users := &stubUserResolver{err: errors.New("user not found")}
	pub := &stubPublisher{}
	handler := webhook.NewCreateSessionHandler(
		fixture.newValidator(t, testCreateSessionAud), users, pub, newLogger(t),
	)

	body := createSessionBody(t, fixture, "zitadel-sub-unprovisioned")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/create-session", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "a lookup miss must never fail login")
	assert.Equal(t, 1, users.calls)
	assert.Equal(t, 0, pub.calls, "no publish when the sub cannot be resolved")
}

func TestCreateSessionHandler_PublishFailure_DoesNotAffectResponse(t *testing.T) {
	fixture := newJWKSFixture(t)
	users := &stubUserResolver{user: &entity.User{ID: "platform-uuid-1"}}
	pub := &stubPublisher{err: errors.New("nats unavailable")}
	handler := webhook.NewCreateSessionHandler(
		fixture.newValidator(t, testCreateSessionAud), users, pub, newLogger(t),
	)

	body := createSessionBody(t, fixture, "zitadel-sub-123")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/create-session", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)

	// The publish failed but login (the webhook response) still succeeds.
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, pub.calls)
}

func TestCreateSessionHandler_GET_ReturnsMethodNotAllowed(t *testing.T) {
	fixture := newJWKSFixture(t)
	handler := webhook.NewCreateSessionHandler(
		fixture.newValidator(t, testCreateSessionAud),
		&stubUserResolver{}, &stubPublisher{}, newLogger(t),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/create-session", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
}
