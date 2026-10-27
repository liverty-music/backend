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
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubResponse is one canned Stripe answer.
type stubResponse struct {
	status int
	body   map[string]any
}

// stripeStub is a minimal Stripe PaymentIntents API. Each operation answers
// with its canned response; an operation without one answers 404. It records
// the idempotency keys and the create forms (the last one in createForm).
type stripeStub struct {
	mu                                sync.Mutex
	create, retrieve, capture, cancel *stubResponse
	keys                              []string
	createForm                        url.Values
	createForms                       []url.Values
	captures, cancels                 int
}

func (s *stripeStub) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		s.keys = append(s.keys, key)
	}
	var resp *stubResponse
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
		s.createForms = append(s.createForms, s.createForm)
		resp = s.create
	case r.Method == http.MethodGet:
		resp = s.retrieve
	}
	if resp == nil {
		resp = &stubResponse{status: http.StatusNotFound, body: stripeErrorBody("invalid_request_error", "resource_missing")}
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

func ok(body map[string]any) *stubResponse {
	return &stubResponse{status: http.StatusOK, body: body}
}

// unexpectedState is Stripe refusing a capture or cancel for the
// PaymentIntent's current state.
var unexpectedState = &stubResponse{
	status: http.StatusBadRequest,
	body:   stripeErrorBody("invalid_request_error", "payment_intent_unexpected_state"),
}

func newStubRepo(t *testing.T, stub *stripeStub, policy payment.CardAuthorizationPolicy) *payment.StripeCardAuthorizationRepository {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	return payment.NewStripeCardAuthorizationRepositoryForTest("sk_test_stub", srv.URL, policy, mustLogger(t))
}

// unreachable returns a repository whose Stripe API refuses connections, and
// a context that bounds the client's network retries. Port 1 is never served,
// so no other test's server can answer it.
func unreachable(t *testing.T) (*payment.StripeCardAuthorizationRepository, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return payment.NewStripeCardAuthorizationRepositoryForTest("sk_test_stub", "http://127.0.0.1:1",
		payment.CheckoutCardAuthorizationPolicy, mustLogger(t)), ctx
}

func TestStripeCardAuthorizationRepository_Create(t *testing.T) {
	t.Parallel()
	checkout := entity.CardAuthorizationPurchase{
		Purpose: entity.CardAuthorizationPurposeCheckout, CheckoutID: "checkout-1",
		UserID: "user-1", TicketTypeID: "type-1", EventID: "event-1", TicketCount: 2,
	}

	t.Run("card authorization opened for a checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/create "Card authorization opened"
		stub := &stripeStub{create: ok(paymentIntentBody("requires_payment_method", 6000, "jpy", ""))}
		repo := newStubRepo(t, stub, payment.CheckoutCardAuthorizationPolicy)

		got, err := repo.Create(context.Background(), checkout, 6000)

		require.NoError(t, err)
		assert.Equal(t, "pi_stub", got.ID)
		assert.Equal(t, "pi_stub_secret_stub", got.Secret)
		assert.Equal(t, int64(6000), got.Amount)
		assert.Equal(t, entity.CardAuthorizationStatusPending, got.Status)
		assert.Equal(t, "6000", stub.createForm.Get("amount"))
		assert.Equal(t, "jpy", stub.createForm.Get("currency"))
		assert.Equal(t, "manual", stub.createForm.Get("capture_method"), "nothing is charged until capture")
		assert.Equal(t, "checkout-1", stub.createForm.Get("metadata[checkout_id]"))
		assert.Equal(t, "user-1", stub.createForm.Get("metadata[user_id]"))
		assert.Equal(t, "type-1", stub.createForm.Get("metadata[ticket_type_id]"))
		assert.Equal(t, "2", stub.createForm.Get("metadata[ticket_count]"))
		assert.Equal(t, "event-1", stub.createForm.Get("metadata[event_id]"))
		assert.Zero(t, stub.captures)
	})

	t.Run("retried after a lost response", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/create "Retried after a lost response"
		// The idempotency key is the Checkout's id, so Stripe replays the first
		// PaymentIntent for a retried Create.
		stub := &stripeStub{create: ok(paymentIntentBody("requires_payment_method", 6000, "jpy", ""))}
		repo := newStubRepo(t, stub, payment.CheckoutCardAuthorizationPolicy)

		_, err := repo.Create(context.Background(), checkout, 6000)
		require.NoError(t, err)
		_, err = repo.Create(context.Background(), checkout, 6000)
		require.NoError(t, err)

		assert.Equal(t, []string{"ticket-sale-authorize:checkout-1", "ticket-sale-authorize:checkout-1"}, stub.keys)
	})

	t.Run("lottery entry authorized again", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/create "Lottery entry authorized again"
		// A lottery entry's Create sets no idempotency key of its own (the
		// Stripe client adds a random one per call for its network retries):
		// each call opens a new PaymentIntent carrying its own count.
		stub := &stripeStub{create: ok(paymentIntentBody("requires_payment_method", 16000, "jpy", ""))}
		repo := newStubRepo(t, stub, payment.LotteryCardAuthorizationPolicy)
		code := entity.ReferralCode("member-a")
		entry := func(count int) entity.CardAuthorizationPurchase {
			return entity.CardAuthorizationPurchase{
				Purpose: entity.CardAuthorizationPurposeLotteryEntry, UserID: "user-1", TicketTypeID: "type-1",
				EventID: "event-1", TicketCount: count, ReferralCode: &code,
			}
		}

		_, err := repo.Create(context.Background(), entry(2), 16000)
		require.NoError(t, err)
		_, err = repo.Create(context.Background(), entry(3), 24000)
		require.NoError(t, err)

		require.Len(t, stub.keys, 2)
		assert.NotEqual(t, stub.keys[0], stub.keys[1], "each call is a new attempt")
		for _, key := range stub.keys {
			assert.NotContains(t, key, "authorize", "no purchase-derived key is sent")
		}
		require.Len(t, stub.createForms, 2)
		for i, count := range []string{"2", "3"} {
			form := stub.createForms[i]
			assert.Equal(t, count, form.Get("metadata[ticket_count]"))
			assert.Equal(t, "user-1", form.Get("metadata[user_id]"))
			assert.Equal(t, "type-1", form.Get("metadata[ticket_type_id]"))
			assert.Equal(t, "event-1", form.Get("metadata[event_id]"))
			assert.Equal(t, "member-a", form.Get("metadata[referral_code]"))
			assert.Empty(t, form.Get("metadata[checkout_id]"))
		}
	})

	t.Run("non-positive amount", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/create "Non-positive amount"
		for _, amount := range []int64{0, -1} {
			stub := &stripeStub{create: ok(paymentIntentBody("requires_payment_method", 1, "jpy", ""))}
			repo := newStubRepo(t, stub, payment.CheckoutCardAuthorizationPolicy)

			_, err := repo.Create(context.Background(), checkout, amount)

			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			assert.Nil(t, stub.createForm, "no card authorization is opened")
		}
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/create "Payment provider unavailable"
		repo, ctx := unreachable(t)

		_, err := repo.Create(ctx, checkout, 6000)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestStripeCardAuthorizationRepository_Get(t *testing.T) {
	t.Parallel()

	t.Run("waiting for card authentication", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/get "Waiting for card authentication"
		repo := newStubRepo(t, &stripeStub{retrieve: ok(paymentIntentBody("requires_action", 16000, "jpy", ""))}, payment.LotteryCardAuthorizationPolicy)

		got, err := repo.Get(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationStatusPending, got.Status)
		assert.Equal(t, int64(16000), got.Amount)
		assert.Equal(t, "pi_stub_secret_stub", got.Secret, "the fan can try again")
	})

	t.Run("authorized card authorization", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/get "Authorized card authorization"
		body := paymentIntentBody("requires_capture", 16000, "jpy", "visa")
		body["metadata"] = map[string]any{
			"user_id": "user-1", "ticket_type_id": "type-1", "ticket_count": "2", "event_id": "event-1", "referral_code": "member-a",
		}
		stub := &stripeStub{retrieve: ok(body)}
		repo := newStubRepo(t, stub, payment.LotteryCardAuthorizationPolicy)

		got, err := repo.Get(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationStatusAuthorized, got.Status)
		assert.Equal(t, int64(16000), got.Amount)
		assert.Equal(t, entity.CardBrand("visa"), got.CardBrand)
		code := entity.ReferralCode("member-a")
		assert.Equal(t, entity.CardAuthorizationPurchase{
			Purpose: entity.CardAuthorizationPurposeLotteryEntry, UserID: "user-1", TicketTypeID: "type-1",
			EventID: "event-1", TicketCount: 2, ReferralCode: &code,
		}, got.Purchase, "the purchase it was opened with")
		assert.Zero(t, stub.captures, "nothing is charged")
		// @spec components/entity/card-authorization/get "Not captured"
		assert.Nil(t, got.CapturedPayment)
	})

	t.Run("captured payment", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/get "Captured payment"
		repo := newStubRepo(t, &stripeStub{retrieve: ok(paymentIntentBody("succeeded", 16000, "jpy", "visa"))}, payment.LotteryCardAuthorizationPolicy)

		got, err := repo.Get(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationStatusCaptured, got.Status)
		require.NotNil(t, got.CapturedPayment)
		assert.Equal(t, &entity.CapturedPayment{
			Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_stub",
			AmountJPY: 16000, Currency: "JPY", CardBrand: "visa", CardLast4: "4242",
		}, got.CapturedPayment)
	})

	t.Run("released card authorization has no captured payment", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/get "Not captured"
		repo := newStubRepo(t, &stripeStub{retrieve: ok(paymentIntentBody("canceled", 16000, "jpy", "visa"))}, payment.LotteryCardAuthorizationPolicy)

		got, err := repo.Get(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationStatusReleased, got.Status)
		assert.Nil(t, got.CapturedPayment)
	})

	t.Run("checkout's purchase", func(t *testing.T) {
		t.Parallel()
		body := paymentIntentBody("requires_capture", 6000, "jpy", "visa")
		body["metadata"] = map[string]any{
			"checkout_id": "checkout-1", "user_id": "user-1", "ticket_type_id": "type-1", "ticket_count": "2", "event_id": "event-1",
		}
		repo := newStubRepo(t, &stripeStub{retrieve: ok(body)}, payment.CheckoutCardAuthorizationPolicy)

		got, err := repo.Get(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationPurchase{
			Purpose: entity.CardAuthorizationPurposeCheckout, CheckoutID: "checkout-1", UserID: "user-1",
			TicketTypeID: "type-1", EventID: "event-1", TicketCount: 2,
		}, got.Purchase)
	})

	t.Run("payment intent without a purchase", func(t *testing.T) {
		t.Parallel()
		repo := newStubRepo(t, &stripeStub{retrieve: ok(paymentIntentBody("requires_capture", 6000, "jpy", "visa"))}, payment.LotteryCardAuthorizationPolicy)

		got, err := repo.Get(context.Background(), "pi_stub")

		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationPurposeUnspecified, got.Purchase.Purpose)
	})

	t.Run("foreign currency", func(t *testing.T) {
		t.Parallel()
		repo := newStubRepo(t, &stripeStub{retrieve: ok(paymentIntentBody("requires_capture", 16000, "usd", "visa"))}, payment.LotteryCardAuthorizationPolicy)

		_, err := repo.Get(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("unknown id", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/get "Unknown id"
		_, err := newStubRepo(t, &stripeStub{}, payment.LotteryCardAuthorizationPolicy).Get(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/get "Payment provider unavailable"
		repo, ctx := unreachable(t)

		_, err := repo.Get(ctx, "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestStripeCardAuthorizationRepository_Capture(t *testing.T) {
	t.Parallel()

	t.Run("card authorization captured", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/capture "Card authorization captured"
		stub := &stripeStub{capture: ok(paymentIntentBody("succeeded", 16000, "jpy", "visa"))}
		repo := newStubRepo(t, stub, payment.LotteryCardAuthorizationPolicy)

		require.NoError(t, repo.Capture(context.Background(), "pi_stub"))

		assert.Equal(t, 1, stub.captures)
		assert.Equal(t, []string{"lottery-capture:pi_stub"}, stub.keys)
	})

	t.Run("repeated capture", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/capture "Repeated capture"
		// @spec components/entity/card-authorization/capture "Repeated days later"
		// Past Stripe's idempotency window a second capture is refused for the
		// state; the card authorization reads back as captured, so it succeeds.
		stub := &stripeStub{capture: unexpectedState, retrieve: ok(paymentIntentBody("succeeded", 6000, "jpy", "visa"))}
		repo := newStubRepo(t, stub, payment.CheckoutCardAuthorizationPolicy)

		require.NoError(t, repo.Capture(context.Background(), "pi_stub"))

		assert.Equal(t, 1, stub.captures, "no second charge attempt")
		assert.Equal(t, []string{"ticket-sale-capture:pi_stub"}, stub.keys, "the key is deterministic in the card authorization")
	})

	t.Run("no longer capturable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/capture "No longer capturable"
		released := &stripeStub{capture: unexpectedState, retrieve: ok(paymentIntentBody("canceled", 16000, "jpy", "visa"))}
		closedCard := &stripeStub{
			capture:  &stubResponse{status: http.StatusPaymentRequired, body: stripeErrorBody("card_error", "card_declined")},
			retrieve: ok(paymentIntentBody("requires_capture", 16000, "jpy", "visa")),
		}
		for _, stub := range []*stripeStub{released, closedCard} {
			err := newStubRepo(t, stub, payment.LotteryCardAuthorizationPolicy).Capture(context.Background(), "pi_stub")

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		}
	})

	t.Run("capture already in progress", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/capture "Capture already in progress"
		stub := &stripeStub{
			capture:  &stubResponse{status: http.StatusConflict, body: stripeErrorBody("idempotency_error", "idempotency_key_in_use")},
			retrieve: ok(paymentIntentBody("requires_capture", 6000, "jpy", "visa")),
		}

		err := newStubRepo(t, stub, payment.CheckoutCardAuthorizationPolicy).Capture(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("unknown id", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/capture "Unknown id"
		err := newStubRepo(t, &stripeStub{}, payment.LotteryCardAuthorizationPolicy).Capture(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		t.Parallel()
		repo, ctx := unreachable(t)

		assert.ErrorIs(t, repo.Capture(ctx, "pi_stub"), apperr.ErrUnavailable)
	})
}

func TestStripeCardAuthorizationRepository_Release(t *testing.T) {
	t.Parallel()

	t.Run("card authorization released", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/release "Card authorization released"
		stub := &stripeStub{cancel: ok(paymentIntentBody("canceled", 16000, "jpy", "visa"))}
		repo := newStubRepo(t, stub, payment.LotteryCardAuthorizationPolicy)

		require.NoError(t, repo.Release(context.Background(), "pi_stub"))

		assert.Equal(t, 1, stub.cancels)
		assert.Zero(t, stub.captures, "nothing is charged")
		assert.Equal(t, []string{"lottery-cancel:pi_stub"}, stub.keys)
	})

	t.Run("repeated release", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/release "Repeated release"
		stub := &stripeStub{cancel: unexpectedState, retrieve: ok(paymentIntentBody("canceled", 6000, "jpy", "visa"))}

		assert.NoError(t, newStubRepo(t, stub, payment.CheckoutCardAuthorizationPolicy).Release(context.Background(), "pi_stub"))
	})

	t.Run("captured card authorization", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/release "Captured card authorization"
		stub := &stripeStub{cancel: unexpectedState, retrieve: ok(paymentIntentBody("succeeded", 6000, "jpy", "visa"))}

		err := newStubRepo(t, stub, payment.CheckoutCardAuthorizationPolicy).Release(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("unknown id", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization/release "Unknown id"
		err := newStubRepo(t, &stripeStub{}, payment.LotteryCardAuthorizationPolicy).Release(context.Background(), "pi_stub")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		t.Parallel()
		repo, ctx := unreachable(t)

		assert.ErrorIs(t, repo.Release(ctx, "pi_stub"), apperr.ErrUnavailable)
	})
}
