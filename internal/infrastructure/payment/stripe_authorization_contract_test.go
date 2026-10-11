package payment_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lotteryStubResponse is one canned Stripe answer.
type lotteryStubResponse struct {
	status int
	body   map[string]any
}

// lotteryStub is a minimal Stripe PaymentIntents API for the lottery card-hold
// port. Each operation answers with its canned response; an operation without
// one answers 404. It records the idempotency keys and the create form.
type lotteryStub struct {
	mu                                sync.Mutex
	create, retrieve, capture, cancel *lotteryStubResponse
	keys                              []string
	createForm                        url.Values
	captures, cancels                 int
}

func (s *lotteryStub) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		s.keys = append(s.keys, key)
	}
	var resp *lotteryStubResponse
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/capture"):
		s.captures++
		resp = s.capture
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
		s.cancels++
		resp = s.cancel
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/payment_intents"):
		raw, _ := io.ReadAll(r.Body)
		s.createForm, _ = url.ParseQuery(string(raw))
		resp = s.create
	case r.Method == http.MethodGet:
		resp = s.retrieve
	}
	if resp == nil {
		resp = &lotteryStubResponse{status: http.StatusNotFound, body: stripeErrorBody("invalid_request_error", "resource_missing")}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_ = json.NewEncoder(w).Encode(resp.body)
}

// paymentIntentBody is a PaymentIntent in the given status, amount, currency
// and card brand.
func paymentIntentBody(status string, amount int64, currency, brand string) map[string]any {
	return map[string]any{
		"id": "pi_stub", "object": "payment_intent", "status": status,
		"amount": amount, "amount_received": amount, "currency": currency,
		"client_secret": "pi_stub_secret_stub",
		"latest_charge": map[string]any{
			"id": "ch_stub", "object": "charge",
			"payment_method_details": map[string]any{"type": "card", "card": map[string]any{"brand": brand, "last4": "4242"}},
		},
	}
}

func stripeErrorBody(errType, code string) map[string]any {
	return map[string]any{"error": map[string]any{"type": errType, "code": code, "message": "stub"}}
}

func ok(body map[string]any) *lotteryStubResponse {
	return &lotteryStubResponse{status: http.StatusOK, body: body}
}

func newLotteryStubPort(t *testing.T, stub *lotteryStub) *payment.StripeAuthorizationPort {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	return payment.NewStripeAuthorizationPortForTest("sk_test_stub", srv.URL, mustLogger(t))
}

// newUnreachablePort returns a port whose Stripe API refuses connections.
func newUnreachablePort(t *testing.T) *payment.StripeAuthorizationPort {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return payment.NewStripeAuthorizationPortForTest("sk_test_stub", srv.URL, mustLogger(t))
}

func TestStripeAuthorizationPort_CreateAuthorization(t *testing.T) {
	t.Parallel()

	t.Run("hold opened", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/create-authorization "Hold opened"
		stub := &lotteryStub{create: ok(paymentIntentBody("requires_payment_method", 16000, "jpy", ""))}
		port := newLotteryStubPort(t, stub)

		ref, secret, err := port.CreateAuthorization(context.Background(), 16000)

		require.NoError(t, err)
		assert.Equal(t, "pi_stub", ref)
		assert.Equal(t, "pi_stub_secret_stub", secret)
		assert.Equal(t, "16000", stub.createForm.Get("amount"))
		assert.Equal(t, "jpy", stub.createForm.Get("currency"))
		assert.Equal(t, "manual", stub.createForm.Get("capture_method"), "nothing is charged until capture")
		assert.Zero(t, stub.captures)
	})

	t.Run("non-positive amount", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/create-authorization "Non-positive amount"
		for _, amount := range []int64{0, -1} {
			stub := &lotteryStub{create: ok(paymentIntentBody("requires_payment_method", 1, "jpy", ""))}
			port := newLotteryStubPort(t, stub)

			_, _, err := port.CreateAuthorization(context.Background(), amount)

			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			assert.Nil(t, stub.createForm, "no hold is opened")
		}
	})

	t.Run("card payments unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/create-authorization "Card payments unavailable"
		_, _, err := newUnreachablePort(t).CreateAuthorization(context.Background(), 16000)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestStripeAuthorizationPort_VerifyAuthorization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		retrieve *lotteryStubResponse
		expected int64
		wantErr  error
	}{
		{
			// @spec components/entity/lottery-entry/verify-authorization "Valid hold"
			name:     "valid hold",
			retrieve: ok(paymentIntentBody("requires_capture", 16000, "jpy", "visa")),
			expected: 16000,
		},
		{
			// @spec components/entity/lottery-entry/verify-authorization "Not authenticated"
			name:     "not authenticated",
			retrieve: ok(paymentIntentBody("requires_action", 16000, "jpy", "visa")),
			expected: 16000,
			wantErr:  apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/entity/lottery-entry/verify-authorization "Amount mismatch"
			name:     "amount mismatch",
			retrieve: ok(paymentIntentBody("requires_capture", 8000, "jpy", "visa")),
			expected: 16000,
			wantErr:  apperr.ErrInvalidArgument,
		},
		{
			// @spec components/entity/lottery-entry/verify-authorization "Foreign currency"
			name:     "foreign currency",
			retrieve: ok(paymentIntentBody("requires_capture", 16000, "usd", "visa")),
			expected: 16000,
			wantErr:  apperr.ErrInvalidArgument,
		},
		{
			// @spec components/entity/lottery-entry/verify-authorization "American Express card"
			name:     "American Express card",
			retrieve: ok(paymentIntentBody("requires_capture", 16000, "jpy", "amex")),
			expected: 16000,
			wantErr:  apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/entity/lottery-entry/verify-authorization "Unknown reference"
			name:     "unknown reference",
			expected: 16000,
			wantErr:  apperr.ErrNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			port := newLotteryStubPort(t, &lotteryStub{retrieve: tt.retrieve})

			err := port.VerifyAuthorization(context.Background(), "pi_stub", tt.expected)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestStripeAuthorizationPort_CaptureAuthorization(t *testing.T) {
	t.Parallel()
	unexpectedState := &lotteryStubResponse{
		status: http.StatusBadRequest,
		body:   stripeErrorBody("invalid_request_error", "payment_intent_unexpected_state"),
	}

	t.Run("hold captured", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/capture-authorization "Hold captured"
		stub := &lotteryStub{capture: ok(paymentIntentBody("succeeded", 16000, "jpy", "visa"))}
		port := newLotteryStubPort(t, stub)

		err := port.CaptureAuthorization(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, 1, stub.captures)
		assert.Equal(t, []string{"lottery-capture:pi_stub"}, stub.keys)
	})

	t.Run("repeated capture", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/capture-authorization "Repeated capture"
		// Past Stripe's idempotency window a second capture is refused for the
		// state; the hold reads back as captured, so it succeeds.
		stub := &lotteryStub{capture: unexpectedState, retrieve: ok(paymentIntentBody("succeeded", 16000, "jpy", "visa"))}
		port := newLotteryStubPort(t, stub)

		err := port.CaptureAuthorization(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, 1, stub.captures, "no second charge attempt")
		assert.Equal(t, []string{"lottery-capture:pi_stub"}, stub.keys, "the key is deterministic in the hold")
	})

	t.Run("hold no longer capturable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/capture-authorization "Hold no longer capturable"
		released := &lotteryStub{capture: unexpectedState, retrieve: ok(paymentIntentBody("canceled", 16000, "jpy", "visa"))}
		closedCard := &lotteryStub{
			capture:  &lotteryStubResponse{status: http.StatusPaymentRequired, body: stripeErrorBody("card_error", "card_declined")},
			retrieve: ok(paymentIntentBody("requires_capture", 16000, "jpy", "visa")),
		}
		for _, stub := range []*lotteryStub{released, closedCard} {
			err := newLotteryStubPort(t, stub).CaptureAuthorization(context.Background(), "pi_stub")

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		}
	})

	t.Run("unknown reference", func(t *testing.T) {
		t.Parallel()
		err := newLotteryStubPort(t, &lotteryStub{}).CaptureAuthorization(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("card payments unavailable", func(t *testing.T) {
		t.Parallel()
		err := newUnreachablePort(t).CaptureAuthorization(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestStripeAuthorizationPort_CancelAuthorization(t *testing.T) {
	t.Parallel()
	unexpectedState := &lotteryStubResponse{
		status: http.StatusBadRequest,
		body:   stripeErrorBody("invalid_request_error", "payment_intent_unexpected_state"),
	}

	t.Run("hold released", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/cancel-authorization "Hold released"
		stub := &lotteryStub{cancel: ok(paymentIntentBody("canceled", 16000, "jpy", "visa"))}
		port := newLotteryStubPort(t, stub)

		err := port.CancelAuthorization(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, 1, stub.cancels)
		assert.Zero(t, stub.captures, "nothing is charged")
		assert.Equal(t, []string{"lottery-cancel:pi_stub"}, stub.keys)
	})

	t.Run("repeated release", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/cancel-authorization "Repeated release"
		stub := &lotteryStub{cancel: unexpectedState, retrieve: ok(paymentIntentBody("canceled", 16000, "jpy", "visa"))}
		port := newLotteryStubPort(t, stub)

		err := port.CancelAuthorization(context.Background(), "pi_stub")

		assert.NoError(t, err)
	})

	t.Run("captured hold", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/cancel-authorization "Captured hold"
		stub := &lotteryStub{cancel: unexpectedState, retrieve: ok(paymentIntentBody("succeeded", 16000, "jpy", "visa"))}
		port := newLotteryStubPort(t, stub)

		err := port.CancelAuthorization(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("unknown reference", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/cancel-authorization "Unknown reference"
		err := newLotteryStubPort(t, &lotteryStub{}).CancelAuthorization(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("card payments unavailable", func(t *testing.T) {
		t.Parallel()
		err := newUnreachablePort(t).CancelAuthorization(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestStripeAuthorizationPort_GetClientSecret(t *testing.T) {
	t.Parallel()

	t.Run("open hold", func(t *testing.T) {
		t.Parallel()
		port := newLotteryStubPort(t, &lotteryStub{retrieve: ok(paymentIntentBody("requires_payment_method", 16000, "jpy", ""))})

		got, err := port.GetClientSecret(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, "pi_stub_secret_stub", got)
	})

	t.Run("unknown reference", func(t *testing.T) {
		t.Parallel()
		_, err := newLotteryStubPort(t, &lotteryStub{}).GetClientSecret(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("card payments unavailable", func(t *testing.T) {
		t.Parallel()
		_, err := newUnreachablePort(t).GetClientSecret(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}
