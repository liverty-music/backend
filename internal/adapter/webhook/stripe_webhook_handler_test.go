package webhook_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v87"
	stripewh "github.com/stripe/stripe-go/v87/webhook"

	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
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

// stubWebhookSvc is a minimal stand-in for usecase.StripeWebhookService. Its
// ApplyOnce keeps the processed event ids in memory, as the real service keeps
// them in the processed-event table.
type stubWebhookSvc struct {
	handleFn  func(ctx context.Context, event usecase.StripeWebhookEvent) error
	processed map[string]bool
}

func (s *stubWebhookSvc) HandleEvent(ctx context.Context, event usecase.StripeWebhookEvent) error {
	if s.handleFn != nil {
		return s.handleFn(ctx, event)
	}
	return nil
}

func (s *stubWebhookSvc) ApplyOnce(ctx context.Context, providerEventID string, _ usecase.StripeEventType, apply func(ctx context.Context) error) error {
	if s.processed[providerEventID] {
		return nil
	}
	if err := apply(ctx); err != nil {
		return err
	}
	if s.processed == nil {
		s.processed = map[string]bool{}
	}
	s.processed[providerEventID] = true
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

	h := webhook.NewStripeWebhookHandler("whsec_correct_secret", &stubWebhookSvc{}, usecasemocks.NewMockLotteryEntryUseCase(t), newTestLoggerWH(t))

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

	h := webhook.NewStripeWebhookHandler("", &stubWebhookSvc{}, usecasemocks.NewMockLotteryEntryUseCase(t), newTestLoggerWH(t))

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

	h := webhook.NewStripeWebhookHandler("whsec_secret", &stubWebhookSvc{}, usecasemocks.NewMockLotteryEntryUseCase(t), newTestLoggerWH(t))

	req := httptest.NewRequest(http.MethodGet, "/stripe-webhook", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ─────────────────────────────────────────────────────────────────────────────
// Valid signature, charge.dispute.created → 200; handler receives ProviderChargeID
// and ProviderPaymentID extracted from the Dispute object.
//
// Fix #1 verification: the handler must extract *both* fields from the event
// data and pass them through. The dispute object shape is:
//   data.object = { "id": "dp_...", "charge": "ch_...", "payment_intent": "pi_..." }
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhookHandler_ValidSignature_DisputeExtracts_ChargeAndPI(t *testing.T) {
	t.Parallel()

	const (
		secret            = "whsec_test_secret_valid"
		eventID           = "evt_valid_001"
		providerDisputeID = "dp_test"
		providerChargeID  = "ch_dispute_test"
		providerPaymentID = "pi_dispute_test"
	)

	var gotEvent usecase.StripeWebhookEvent
	svc := &stubWebhookSvc{
		handleFn: func(_ context.Context, event usecase.StripeWebhookEvent) error {
			gotEvent = event
			return nil
		},
	}

	lotteryEntries := usecasemocks.NewMockLotteryEntryUseCase(t)
	h := webhook.NewStripeWebhookHandler(secret, svc, lotteryEntries, newTestLoggerWH(t))

	// Dispute object: data.object.id = dp_, data.object.charge = ch_,
	// data.object.payment_intent = pi_ (string form, no expansion).
	payload := map[string]any{
		"id":     eventID,
		"object": "event",
		"type":   "charge.dispute.created",
		"data": map[string]any{
			"object": map[string]any{
				"id":             providerDisputeID,
				"charge":         providerChargeID,
				"payment_intent": providerPaymentID,
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
	assert.Equal(t, providerChargeID, gotEvent.ProviderChargeID, "ProviderChargeID must be extracted from dispute.charge")
	assert.Equal(t, providerPaymentID, gotEvent.ProviderPaymentID, "ProviderPaymentID must be extracted from dispute.payment_intent")
}

// ─────────────────────────────────────────────────────────────────────────────
// Valid signature, charge.refunded → ProviderChargeID extracted (charge.id = object.id)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhookHandler_ValidSignature_ChargeRefunded_ExtractsProviderChargeID(t *testing.T) {
	t.Parallel()

	const (
		secret            = "whsec_test_charge_refund"
		providerChargeID  = "ch_refunded_test"
		providerPaymentID = "pi_refunded_test"
	)

	var gotEvent usecase.StripeWebhookEvent
	svc := &stubWebhookSvc{
		handleFn: func(_ context.Context, event usecase.StripeWebhookEvent) error {
			gotEvent = event
			return nil
		},
	}

	lotteryEntries := usecasemocks.NewMockLotteryEntryUseCase(t)
	h := webhook.NewStripeWebhookHandler(secret, svc, lotteryEntries, newTestLoggerWH(t))

	// For charge.refunded data.object IS a Charge: id = ch_, payment_intent = pi_.
	payload := map[string]any{
		"id":     "evt_refund_001",
		"object": "event",
		"type":   "charge.refunded",
		"data": map[string]any{
			"object": map[string]any{
				"id":             providerChargeID,
				"payment_intent": providerPaymentID,
			},
		},
	}
	body, sigHeader := signedBody(t, payload, secret)

	req := httptest.NewRequest(http.MethodPost, "/stripe-webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", sigHeader)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, providerChargeID, gotEvent.ProviderChargeID, "ProviderChargeID must be object.id for charge.refunded")
	assert.Equal(t, providerPaymentID, gotEvent.ProviderPaymentID, "ProviderPaymentID must be extracted for charge.refunded")
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

	lotteryEntries := usecasemocks.NewMockLotteryEntryUseCase(t)
	h := webhook.NewStripeWebhookHandler(secret, svc, lotteryEntries, newTestLoggerWH(t))

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

// TestStripeWebhookHandler_PaymentIntentSucceeded checks that a completed
// charge notice passes the PaymentIntent id (data.object.id) to the service,
// and that a fulfillment failure is answered with an error status so Stripe
// delivers it again.
func TestStripeWebhookHandler_PaymentIntentSucceeded(t *testing.T) {
	t.Parallel()
	const secret = "whsec_test_secret_pi"

	send := func(t *testing.T, svc *stubWebhookSvc) *httptest.ResponseRecorder {
		t.Helper()
		payload := map[string]any{
			"id":     "evt_pi_001",
			"object": "event",
			"type":   "payment_intent.succeeded",
			"data":   map[string]any{"object": map[string]any{"id": "pi_charged", "object": "payment_intent", "status": "succeeded"}},
		}
		body, sigHeader := signedBody(t, payload, secret)
		req := httptest.NewRequest(http.MethodPost, "/stripe-webhook", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", sigHeader)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		webhook.NewStripeWebhookHandler(secret, svc, usecasemocks.NewMockLotteryEntryUseCase(t), newTestLoggerWH(t)).ServeHTTP(w, req)
		return w
	}

	t.Run("passes the PaymentIntent id", func(t *testing.T) {
		t.Parallel()
		var got usecase.StripeWebhookEvent
		w := send(t, &stubWebhookSvc{handleFn: func(_ context.Context, e usecase.StripeWebhookEvent) error { got = e; return nil }})

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, usecase.StripeEventPaymentIntentSucceeded, got.Type)
		assert.Equal(t, "pi_charged", got.ProviderPaymentID)
	})

	t.Run("a failed fulfillment is redelivered", func(t *testing.T) {
		t.Parallel()
		w := send(t, &stubWebhookSvc{handleFn: func(context.Context, usecase.StripeWebhookEvent) error {
			return errors.New("fulfillment failed")
		}})

		assert.GreaterOrEqual(t, w.Code, http.StatusInternalServerError)
	})
}

// TestStripeWebhookHandler_CardAuthorizationAuthorized checks that a notice
// that a card authorization became Authorized runs LotteryEntryUseCase.Confirm
// without a calling fan, at most once per notice, acknowledging a rejection
// and answering an error status for any other failure.
func TestStripeWebhookHandler_CardAuthorizationAuthorized(t *testing.T) {
	t.Parallel()
	const (
		secret = "whsec_test_secret_capturable"
		piID   = "pi_authorized"
	)

	send := func(t *testing.T, svc *stubWebhookSvc, lotteryEntries *usecasemocks.MockLotteryEntryUseCase, eventID string) *httptest.ResponseRecorder {
		t.Helper()
		payload := map[string]any{
			"id":     eventID,
			"object": "event",
			"type":   "payment_intent.amount_capturable_updated",
			"data":   map[string]any{"object": map[string]any{"id": piID, "object": "payment_intent", "status": "requires_capture"}},
		}
		body, sigHeader := signedBody(t, payload, secret)
		req := httptest.NewRequest(http.MethodPost, "/stripe-webhook", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", sigHeader)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		webhook.NewStripeWebhookHandler(secret, svc, lotteryEntries, newTestLoggerWH(t)).ServeHTTP(w, req)
		return w
	}
	entered := &entity.LotteryEntry{ID: "entry-1", State: entity.LotteryEntryStateEntered, Order: &entity.Order{CardAuthorizationID: piID}}

	t.Run("fan closed the browser", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Fan closed the browser"
		lotteryEntries := usecasemocks.NewMockLotteryEntryUseCase(t)
		lotteryEntries.EXPECT().Confirm(mock.Anything, piID, (*entity.UserID)(nil)).Return(entered, nil).Once()
		svc := &stubWebhookSvc{handleFn: func(context.Context, usecase.StripeWebhookEvent) error {
			t.Error("the notice must not reach HandleEvent")
			return nil
		}}

		w := send(t, svc, lotteryEntries, "evt_capturable_1")

		assert.Equal(t, http.StatusOK, w.Code)
		assert.True(t, svc.processed["evt_capturable_1"], "the notice is recorded as processed")
	})

	t.Run("entry already made", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Entry already made"
		lotteryEntries := usecasemocks.NewMockLotteryEntryUseCase(t)
		// Confirm returns the entry the fan's Confirm made; a redelivery of
		// the same notice is not applied again.
		lotteryEntries.EXPECT().Confirm(mock.Anything, piID, (*entity.UserID)(nil)).Return(entered, nil).Once()
		svc := &stubWebhookSvc{}

		assert.Equal(t, http.StatusOK, send(t, svc, lotteryEntries, "evt_capturable_2").Code)
		assert.Equal(t, http.StatusOK, send(t, svc, lotteryEntries, "evt_capturable_2").Code)
	})

	t.Run("rejection acknowledged", func(t *testing.T) {
		t.Parallel()
		for _, code := range []codes.Code{codes.PermissionDenied, codes.FailedPrecondition, codes.InvalidArgument} {
			lotteryEntries := usecasemocks.NewMockLotteryEntryUseCase(t)
			lotteryEntries.EXPECT().Confirm(mock.Anything, piID, (*entity.UserID)(nil)).Return(nil, apperr.New(code, "rejected")).Once()
			svc := &stubWebhookSvc{}

			w := send(t, svc, lotteryEntries, "evt_capturable_3")

			assert.Equal(t, http.StatusOK, w.Code, code.String())
			assert.True(t, svc.processed["evt_capturable_3"], code.String())
		}
	})

	t.Run("payment provider unreachable", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Payment provider unreachable"
		lotteryEntries := usecasemocks.NewMockLotteryEntryUseCase(t)
		lotteryEntries.EXPECT().Confirm(mock.Anything, piID, (*entity.UserID)(nil)).
			Return(nil, apperr.New(codes.Unavailable, "stripe unreachable")).Once()
		svc := &stubWebhookSvc{}

		w := send(t, svc, lotteryEntries, "evt_capturable_4")

		assert.Equal(t, http.StatusInternalServerError, w.Code, "the provider delivers the notice again")
		assert.False(t, svc.processed["evt_capturable_4"], "a failed notice is not recorded")
	})
}
