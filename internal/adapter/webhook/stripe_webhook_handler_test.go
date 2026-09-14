package webhook_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
	stripewh "github.com/stripe/stripe-go/v86/webhook"

	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

func newTestLoggerWH(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return logger
}

// stubWebhookSvc is a minimal stand-in for usecase.StripeWebhookService.
type stubWebhookSvc struct {
	handleFn func(ctx context.Context, event usecase.StripeWebhookEvent) error
}

func (s *stubWebhookSvc) HandleEvent(ctx context.Context, event usecase.StripeWebhookEvent) error {
	if s.handleFn != nil {
		return s.handleFn(ctx, event)
	}
	return nil
}

// signedBody builds a raw JSON body and a valid Stripe-Signature header for
// the given signing secret. The payload's api_version is stamped to the SDK's
// pinned version so that stripewh.ConstructEvent does not reject the event.
func signedBody(t *testing.T, payload map[string]any, secret string) ([]byte, string) {
	t.Helper()
	payload["api_version"] = stripe.APIVersion
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	ts := time.Now()
	sigBytes := stripewh.ComputeSignature(ts, body, secret)
	header := fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(sigBytes))
	return body, header
}

// ─────────────────────────────────────────────────────────────────────────────
// Signature verify failure → 401
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhookHandler_InvalidSignature_Returns401(t *testing.T) {
	t.Parallel()

	h := webhook.NewStripeWebhookHandler("whsec_correct_secret", &stubWebhookSvc{}, newTestLoggerWH(t))

	body := []byte(`{"id":"evt_test","object":"event","type":"charge.refunded","data":{"object":{}}}`)
	req := httptest.NewRequest(http.MethodPost, "/stripe-webhook", bytes.NewReader(body))
	ts := time.Now()
	wrongSig := stripewh.ComputeSignature(ts, body, "whsec_wrong_secret")
	req.Header.Set("Stripe-Signature", fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(wrongSig)))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code, "invalid signature must return 401")
}

// ─────────────────────────────────────────────────────────────────────────────
// No signing secret configured → 503
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhookHandler_NoSigningSecret_Returns503(t *testing.T) {
	t.Parallel()

	h := webhook.NewStripeWebhookHandler("", &stubWebhookSvc{}, newTestLoggerWH(t))

	req := httptest.NewRequest(http.MethodPost, "/stripe-webhook", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Stripe-Signature", "t=1,v1=fake")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "unconfigured endpoint must return 503")
}

// ─────────────────────────────────────────────────────────────────────────────
// Wrong HTTP method → 405
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhookHandler_WrongMethod_Returns405(t *testing.T) {
	t.Parallel()

	h := webhook.NewStripeWebhookHandler("whsec_secret", &stubWebhookSvc{}, newTestLoggerWH(t))

	req := httptest.NewRequest(http.MethodGet, "/stripe-webhook", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ─────────────────────────────────────────────────────────────────────────────
// Valid signature, charge.dispute.created → 200; handler receives ChargeRef
// and PaymentIntentRef extracted from the Dispute object.
//
// Fix #1 verification: the handler must extract *both* fields from the event
// data and pass them through. The dispute object shape is:
//   data.object = { "id": "dp_...", "charge": "ch_...", "payment_intent": "pi_..." }
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhookHandler_ValidSignature_DisputeExtracts_ChargeAndPI(t *testing.T) {
	t.Parallel()

	const (
		secret  = "whsec_test_secret_valid"
		eventID = "evt_valid_001"
		dpRef   = "dp_test"
		chRef   = "ch_dispute_test"
		piRef   = "pi_dispute_test"
	)

	var gotEvent usecase.StripeWebhookEvent
	svc := &stubWebhookSvc{
		handleFn: func(_ context.Context, event usecase.StripeWebhookEvent) error {
			gotEvent = event
			return nil
		},
	}

	h := webhook.NewStripeWebhookHandler(secret, svc, newTestLoggerWH(t))

	// Dispute object: data.object.id = dp_, data.object.charge = ch_,
	// data.object.payment_intent = pi_ (string form, no expansion).
	payload := map[string]any{
		"id":     eventID,
		"object": "event",
		"type":   "charge.dispute.created",
		"data": map[string]any{
			"object": map[string]any{
				"id":             dpRef,
				"charge":         chRef,
				"payment_intent": piRef,
			},
		},
	}
	body, sigHeader := signedBody(t, payload, secret)

	req := httptest.NewRequest(http.MethodPost, "/stripe-webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", sigHeader)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "valid webhook must return 200")
	assert.Equal(t, eventID, gotEvent.ProviderEventID)
	assert.Equal(t, usecase.StripeEventChargeDisputeCreated, gotEvent.Type)
	// Fix #1: both fields must be extracted and passed through.
	assert.Equal(t, chRef, gotEvent.ChargeRef, "ChargeRef must be extracted from dispute.charge")
	assert.Equal(t, piRef, gotEvent.PaymentIntentRef, "PaymentIntentRef must be extracted from dispute.payment_intent")
}

// ─────────────────────────────────────────────────────────────────────────────
// Valid signature, charge.refunded → ChargeRef extracted (charge.id = object.id)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhookHandler_ValidSignature_ChargeRefunded_ExtractsChargeRef(t *testing.T) {
	t.Parallel()

	const (
		secret = "whsec_test_charge_refund"
		chRef  = "ch_refunded_test"
		piRef  = "pi_refunded_test"
	)

	var gotEvent usecase.StripeWebhookEvent
	svc := &stubWebhookSvc{
		handleFn: func(_ context.Context, event usecase.StripeWebhookEvent) error {
			gotEvent = event
			return nil
		},
	}

	h := webhook.NewStripeWebhookHandler(secret, svc, newTestLoggerWH(t))

	// For charge.refunded data.object IS a Charge: id = ch_, payment_intent = pi_.
	payload := map[string]any{
		"id":     "evt_refund_001",
		"object": "event",
		"type":   "charge.refunded",
		"data": map[string]any{
			"object": map[string]any{
				"id":             chRef,
				"payment_intent": piRef,
			},
		},
	}
	body, sigHeader := signedBody(t, payload, secret)

	req := httptest.NewRequest(http.MethodPost, "/stripe-webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", sigHeader)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, chRef, gotEvent.ChargeRef, "ChargeRef must be object.id for charge.refunded")
	assert.Equal(t, piRef, gotEvent.PaymentIntentRef, "PaymentIntentRef must be extracted for charge.refunded")
}

// ─────────────────────────────────────────────────────────────────────────────
// Service returns error → 500 (Stripe retries)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhookHandler_ServiceError_Returns500(t *testing.T) {
	t.Parallel()

	const secret = "whsec_test_service_err"

	svc := &stubWebhookSvc{
		handleFn: func(_ context.Context, _ usecase.StripeWebhookEvent) error {
			return fmt.Errorf("internal dispatch error")
		},
	}

	h := webhook.NewStripeWebhookHandler(secret, svc, newTestLoggerWH(t))

	payload := map[string]any{
		"id":     "evt_svc_err",
		"object": "event",
		"type":   "charge.refunded",
		"data":   map[string]any{"object": map[string]any{}},
	}
	body, sigHeader := signedBody(t, payload, secret)

	req := httptest.NewRequest(http.MethodPost, "/stripe-webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", sigHeader)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"a service error must return 500 so Stripe retries")
}
