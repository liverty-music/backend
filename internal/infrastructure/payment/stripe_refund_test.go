package payment_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refundStub is a minimal Stripe Refunds API over one charge of chargeAmount
// yen. Like Stripe, it replays the answer of an earlier request with the same
// idempotency key, refuses a refund larger than what is left of the charge
// (amount_too_large) and one on a fully refunded charge
// (charge_already_refunded).
type refundStub struct {
	mu           sync.Mutex
	chargeAmount int64
	refunded     int64
	byKey        map[string]string
	created      int
}

func newRefundStub(chargeAmount int64) *refundStub {
	return &refundStub{chargeAmount: chargeAmount, byKey: map[string]string{}}
}

func (s *refundStub) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	key := r.Header.Get("Idempotency-Key")
	if id, ok := s.byKey[key]; ok {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "object": "refund"})
		return
	}
	raw, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(raw))
	amount, _ := strconv.ParseInt(form.Get("amount"), 10, 64)
	switch {
	case s.refunded == s.chargeAmount:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(stripeErrorBody("invalid_request_error", "charge_already_refunded"))
		return
	case amount > s.chargeAmount-s.refunded:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(stripeErrorBody("invalid_request_error", "amount_too_large"))
		return
	}
	s.created++
	s.refunded += amount
	id := fmt.Sprintf("re_%d", s.created)
	s.byKey[key] = id
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "object": "refund", "amount": amount})
}

func newStubOrderPayments(t *testing.T, stub *refundStub) *payment.StripeOrderPayments {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	return payment.NewStripeOrderPaymentsForTest("sk_test_stub", srv.URL, mustLogger(t))
}

func TestStripeOrderPayments_CreateRefund(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("refund issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/create-refund "Refund issued"
		stub := newRefundStub(16000)
		orders := newStubOrderPayments(t, stub)

		id, err := orders.CreateRefund(ctx, entity.RefundParams{OrderID: "order-1", ProviderChargeID: "ch_1", Amount: 16000})

		require.NoError(t, err)
		assert.NotEmpty(t, id)
		assert.Equal(t, int64(16000), stub.refunded)
	})

	t.Run("repeated refund", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/create-refund "Repeated refund"
		stub := newRefundStub(16000)
		orders := newStubOrderPayments(t, stub)
		params := entity.RefundParams{OrderID: "order-1", ProviderChargeID: "ch_1", Amount: 16000}

		first, err := orders.CreateRefund(ctx, params)
		require.NoError(t, err)
		second, err := orders.CreateRefund(ctx, params)

		require.NoError(t, err)
		assert.Equal(t, first, second)
		assert.Equal(t, 1, stub.created)
	})

	t.Run("two listings of one order", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/create-refund "Two listings of one order"
		stub := newRefundStub(10000)
		orders := newStubOrderPayments(t, stub)

		a, err := orders.CreateRefund(ctx, entity.RefundParams{OrderID: "order-1", ResaleListingID: "listing-a", ProviderChargeID: "ch_1", Amount: 4500})
		require.NoError(t, err)
		b, err := orders.CreateRefund(ctx, entity.RefundParams{OrderID: "order-1", ResaleListingID: "listing-b", ProviderChargeID: "ch_1", Amount: 4500})

		require.NoError(t, err)
		assert.NotEqual(t, a, b)
		assert.Equal(t, int64(9000), stub.refunded)
	})

	t.Run("more than is left", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/create-refund "More than is left"
		stub := newRefundStub(5000)
		orders := newStubOrderPayments(t, stub)
		_, err := orders.CreateRefund(ctx, entity.RefundParams{OrderID: "order-1", ResaleListingID: "listing-a", ProviderChargeID: "ch_1", Amount: 4500})
		require.NoError(t, err)

		_, err = orders.CreateRefund(ctx, entity.RefundParams{OrderID: "order-1", ProviderChargeID: "ch_1", Amount: 1000})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, int64(4500), stub.refunded)
	})

	t.Run("fully refunded charge", func(t *testing.T) {
		t.Parallel()
		stub := newRefundStub(5000)
		orders := newStubOrderPayments(t, stub)
		_, err := orders.CreateRefund(ctx, entity.RefundParams{OrderID: "order-1", ProviderChargeID: "ch_1", Amount: 5000})
		require.NoError(t, err)

		_, err = orders.CreateRefund(ctx, entity.RefundParams{OrderID: "order-1", ResaleListingID: "listing-a", ProviderChargeID: "ch_1", Amount: 4500})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("non-positive amount", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/create-refund "Non-positive amount"
		stub := newRefundStub(5000)
		orders := newStubOrderPayments(t, stub)

		_, err := orders.CreateRefund(ctx, entity.RefundParams{OrderID: "order-1", ProviderChargeID: "ch_1", Amount: 0})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Zero(t, stub.created)
	})

}
