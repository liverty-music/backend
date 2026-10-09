package payment_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStripe is a minimal Stripe API: it answers capture and cancel with a
// fixed error, and retrieve with a PaymentIntent in a fixed status. It counts
// the captures it received.
type stubStripe struct {
	mu            sync.Mutex
	actionStatus  int
	actionErrType string
	actionErrCode string
	piStatus      string
	captures      int
	keys          []string
}

func (s *stubStripe) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		s.keys = append(s.keys, key)
	}
	if r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/capture") || strings.HasSuffix(r.URL.Path, "/cancel")) {
		if strings.HasSuffix(r.URL.Path, "/capture") {
			s.captures++
		}
		w.WriteHeader(s.actionStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"type": s.actionErrType, "code": s.actionErrCode, "message": "stub",
		}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "pi_stub", "object": "payment_intent", "status": s.piStatus,
		"amount": 6000, "amount_received": 6000, "currency": "jpy",
		"latest_charge": map[string]any{
			"id": "ch_stub", "object": "charge",
			"payment_method_details": map[string]any{"type": "card", "card": map[string]any{"brand": "amex", "last4": "0005"}},
		},
	})
}

func newStubPort(t *testing.T, stub *stubStripe) *payment.StripeReservationAuthorizationPort {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	return payment.NewStripeReservationAuthorizationPortForTest("sk_test_stub", srv.URL, mustLogger(t))
}

func TestStripeReservationAuthorizationPort_CaptureAuthorization(t *testing.T) {
	t.Parallel()

	t.Run("repeated a day later", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/reservation/capture-authorization "Repeated a day later"
		stub := &stubStripe{
			actionStatus: http.StatusBadRequest, actionErrType: "invalid_request_error",
			actionErrCode: "payment_intent_unexpected_state", piStatus: "succeeded",
		}
		port := newStubPort(t, stub)

		got, err := port.CaptureAuthorization(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, &entity.CapturedPayment{
			Provider: entity.PaymentProviderStripe, PaymentIntentRef: "pi_stub",
			AmountJPY: 6000, Currency: "JPY", CardBrand: "amex", CardLast4: "0005",
		}, got)
		assert.Equal(t, 1, stub.captures, "no second charge attempt")
		assert.Contains(t, stub.keys, "ticket-sale-capture:pi_stub")
	})

	t.Run("capture already in progress", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/reservation/capture-authorization "Capture already in progress"
		stub := &stubStripe{
			actionStatus: http.StatusConflict, actionErrType: "idempotency_error",
			actionErrCode: "idempotency_key_in_use", piStatus: "requires_capture",
		}
		port := newStubPort(t, stub)

		_, err := port.CaptureAuthorization(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("hold no longer capturable", func(t *testing.T) {
		t.Parallel()
		stub := &stubStripe{
			actionStatus: http.StatusBadRequest, actionErrType: "invalid_request_error",
			actionErrCode: "payment_intent_unexpected_state", piStatus: "canceled",
		}
		port := newStubPort(t, stub)

		_, err := port.CaptureAuthorization(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("card refused at capture", func(t *testing.T) {
		t.Parallel()
		stub := &stubStripe{
			actionStatus: http.StatusPaymentRequired, actionErrType: "card_error",
			actionErrCode: "card_declined", piStatus: "requires_capture",
		}
		port := newStubPort(t, stub)

		_, err := port.CaptureAuthorization(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("provider error while processing", func(t *testing.T) {
		t.Parallel()
		stub := &stubStripe{
			actionStatus: http.StatusInternalServerError, actionErrType: "api_error", piStatus: "processing",
		}
		port := newStubPort(t, stub)

		_, err := port.CaptureAuthorization(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestStripeReservationAuthorizationPort_CancelAuthorization(t *testing.T) {
	t.Parallel()

	t.Run("already released", func(t *testing.T) {
		t.Parallel()
		stub := &stubStripe{
			actionStatus: http.StatusBadRequest, actionErrType: "invalid_request_error",
			actionErrCode: "payment_intent_unexpected_state", piStatus: "canceled",
		}
		port := newStubPort(t, stub)

		assert.NoError(t, port.CancelAuthorization(context.Background(), "pi_stub"))
		assert.Contains(t, stub.keys, "ticket-sale-cancel:pi_stub")
	})

	t.Run("charged hold", func(t *testing.T) {
		t.Parallel()
		stub := &stubStripe{
			actionStatus: http.StatusBadRequest, actionErrType: "invalid_request_error",
			actionErrCode: "payment_intent_unexpected_state", piStatus: "succeeded",
		}
		port := newStubPort(t, stub)

		assert.ErrorIs(t, port.CancelAuthorization(context.Background(), "pi_stub"), apperr.ErrFailedPrecondition)
	})
}

func TestStripeReservationAuthorizationPort_VerifyAuthorization(t *testing.T) {
	t.Parallel()

	t.Run("American Express card", func(t *testing.T) {
		t.Parallel()
		stub := &stubStripe{piStatus: "requires_capture"}
		port := newStubPort(t, stub)

		assert.NoError(t, port.VerifyAuthorization(context.Background(), "pi_stub", 6000))
	})

	t.Run("amount mismatch", func(t *testing.T) {
		t.Parallel()
		stub := &stubStripe{piStatus: "requires_capture"}
		port := newStubPort(t, stub)

		assert.ErrorIs(t, port.VerifyAuthorization(context.Background(), "pi_stub", 3000), apperr.ErrInvalidArgument)
	})

	t.Run("not authenticated", func(t *testing.T) {
		t.Parallel()
		stub := &stubStripe{piStatus: "requires_action"}
		port := newStubPort(t, stub)

		assert.ErrorIs(t, port.VerifyAuthorization(context.Background(), "pi_stub", 6000), apperr.ErrFailedPrecondition)
	})
}
