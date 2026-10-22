package webhook_test

import (
	"bytes"
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

const (
	testPasskeyEventSigningKey = "test-passkey-event-signing-key"
	testProductOrgID           = "product-org-1"
)

// testAAGUID is an authenticator model id as 16 bytes.
var testAAGUID = []byte{0xfb, 0xfc, 0x30, 0x07, 0x15, 0x4e, 0x4e, 0xcc, 0x8c, 0x0b, 0x6e, 0x02, 0x05, 0x57, 0xd7, 0xbd}

// passkeyEventBody builds a Zitadel Actions v2 event-execution body for a
// passkey event of the identity zitadel-fan-1 in organization owner. inner is
// the event's own payload, base64-encoded as Zitadel sends it.
func passkeyEventBody(t *testing.T, eventType, owner string, inner map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(inner)
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"aggregateID":   "zitadel-fan-1",
		"aggregateType": "user",
		"resourceOwner": owner,
		"event_type":    eventType,
		"created_at":    "2026-10-22T03:04:05.123456Z",
		"userID":        "login-ui-editor",
		"event_payload": base64.StdEncoding.EncodeToString(raw),
	})
	require.NoError(t, err)
	return body
}

func verifiedEventBody(t *testing.T, owner string) []byte {
	t.Helper()
	return passkeyEventBody(t, "user.human.passwordless.token.verified", owner, map[string]any{
		"webAuthNTokenId":   "token-1",
		"webAuthNTokenName": "Apple iPhone, iOS, Mobile Safari",
		"aaguid":            testAAGUID, // marshalled as base64, like Zitadel's []byte
		"signCount":         0,
	})
}

func signedPasskeyRequest(t *testing.T, body []byte, signingKey string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/passkey-event", bytes.NewReader(body))
	req.Header.Set(actions.SigningHeader, actions.ComputeSignatureHeader(time.Now(), body, signingKey))
	return req
}

func newPasskeyEventHandler(t *testing.T, pub *stubPublisher) *webhook.PasskeyEventHandler {
	t.Helper()
	return webhook.NewPasskeyEventHandler(testPasskeyEventSigningKey, testProductOrgID, pub, newLogger(t))
}

func TestPasskeyEventHandler(t *testing.T) {
	t.Parallel()

	wantTime := time.Date(2026, 10, 22, 3, 4, 5, 123456000, time.UTC)

	// @spec components/adapter/fan/api/webhook/passkey-events "Valid signature"
	// @spec components/adapter/fan/api/webhook/passkey-events "Passkey registered"
	t.Run("accepts a signed passkey-added notice and announces an added change with the name", func(t *testing.T) {
		t.Parallel()
		pub := &stubPublisher{}
		rec := httptest.NewRecorder()

		newPasskeyEventHandler(t, pub).ServeHTTP(rec, signedPasskeyRequest(t, verifiedEventBody(t, testProductOrgID), testPasskeyEventSigningKey))

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, 1, pub.calls)
		assert.Equal(t, entity.SubjectUserPasskeyChanged, pub.subject)
		data, ok := pub.data.(entity.UserPasskeyChangedData)
		require.True(t, ok, "published data must be entity.UserPasskeyChangedData")
		assert.Equal(t, entity.UserPasskeyChangedData{
			ProviderUserID:    "zitadel-fan-1",
			ProviderPasskeyID: "token-1",
			Kind:              "added",
			Name:              "Apple iPhone, iOS, Mobile Safari",
			AAGUID:            "fbfc3007-154e-4ecc-8c0b-6e020557d7bd",
			ChangeTime:        wantTime,
		}, data)
	})

	// @spec components/adapter/fan/api/webhook/passkey-events "Forged notice"
	t.Run("rejects a notice with a missing or invalid signature with 401 and announces nothing", func(t *testing.T) {
		t.Parallel()
		pub := &stubPublisher{}
		handler := newPasskeyEventHandler(t, pub)
		body := verifiedEventBody(t, testProductOrgID)

		forged := httptest.NewRecorder()
		handler.ServeHTTP(forged, signedPasskeyRequest(t, body, "wrong-signing-key"))
		unsigned := httptest.NewRecorder()
		handler.ServeHTTP(unsigned, httptest.NewRequest(http.MethodPost, "/passkey-event", bytes.NewReader(body)))

		assert.Equal(t, http.StatusUnauthorized, forged.Code)
		assert.Equal(t, http.StatusUnauthorized, unsigned.Code)
		assert.Equal(t, 0, pub.calls)
	})

	// @spec components/adapter/fan/api/webhook/passkey-events "Passkey removed"
	t.Run("announces a removed change without a name for a passkey-removed notice", func(t *testing.T) {
		t.Parallel()
		pub := &stubPublisher{}
		rec := httptest.NewRecorder()
		body := passkeyEventBody(t, "user.human.passwordless.token.removed", testProductOrgID, map[string]any{
			"webAuthNTokenId": "token-1",
		})

		newPasskeyEventHandler(t, pub).ServeHTTP(rec, signedPasskeyRequest(t, body, testPasskeyEventSigningKey))

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, 1, pub.calls)
		assert.Equal(t, entity.UserPasskeyChangedData{
			ProviderUserID:    "zitadel-fan-1",
			ProviderPasskeyID: "token-1",
			Kind:              "removed",
			ChangeTime:        wantTime,
		}, pub.data)
	})

	// @spec components/adapter/fan/api/webhook/passkey-events "Organizer identity"
	t.Run("answers 200 and announces nothing for an identity of an Organizer's Organization", func(t *testing.T) {
		t.Parallel()
		pub := &stubPublisher{}
		rec := httptest.NewRecorder()

		newPasskeyEventHandler(t, pub).ServeHTTP(rec, signedPasskeyRequest(t, verifiedEventBody(t, "organizer-org-1"), testPasskeyEventSigningKey))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 0, pub.calls)
	})

	t.Run("answers 200 and announces nothing for a notice of another kind", func(t *testing.T) {
		t.Parallel()
		pub := &stubPublisher{}
		rec := httptest.NewRecorder()
		body := passkeyEventBody(t, "user.human.passwordless.check.succeeded", testProductOrgID, map[string]any{})

		newPasskeyEventHandler(t, pub).ServeHTTP(rec, signedPasskeyRequest(t, body, testPasskeyEventSigningKey))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 0, pub.calls)
	})

	// @spec components/adapter/fan/api/webhook/passkey-events "Announcement fails"
	t.Run("answers 500 when the passkey change cannot be announced", func(t *testing.T) {
		t.Parallel()
		pub := &stubPublisher{err: errors.New("nats unavailable")}
		rec := httptest.NewRecorder()

		newPasskeyEventHandler(t, pub).ServeHTTP(rec, signedPasskeyRequest(t, verifiedEventBody(t, testProductOrgID), testPasskeyEventSigningKey))

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, 1, pub.calls)
	})

	t.Run("answers 503 and announces nothing without the product organization id", func(t *testing.T) {
		t.Parallel()
		pub := &stubPublisher{}
		rec := httptest.NewRecorder()
		handler := webhook.NewPasskeyEventHandler(testPasskeyEventSigningKey, "", pub, newLogger(t))

		handler.ServeHTTP(rec, signedPasskeyRequest(t, verifiedEventBody(t, testProductOrgID), testPasskeyEventSigningKey))

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, 0, pub.calls)
	})

	t.Run("refuses a method other than POST", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()

		newPasskeyEventHandler(t, &stubPublisher{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/passkey-event", nil))

		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
}
