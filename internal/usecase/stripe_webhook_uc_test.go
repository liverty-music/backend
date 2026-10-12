package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Stubs
// ─────────────────────────────────────────────────────────────────────────────

type stubProcessedWebhookEventRepo struct {
	isProcessedFn   func(ctx context.Context, id string) (bool, error)
	markProcessedFn func(ctx context.Context, id string) error
}

func (s *stubProcessedWebhookEventRepo) IsProcessed(ctx context.Context, id string) (bool, error) {
	if s.isProcessedFn != nil {
		return s.isProcessedFn(ctx, id)
	}
	return false, nil
}
func (s *stubProcessedWebhookEventRepo) MarkProcessed(ctx context.Context, id string) error {
	if s.markProcessedFn != nil {
		return s.markProcessedFn(ctx, id)
	}
	return nil
}

type stubRefundUC struct {
	refundOrderFn func(ctx context.Context, orderID entity.OrderID, reason usecase.RefundReason, now time.Time) (*entity.Order, error)
}

func (s *stubRefundUC) Refund(ctx context.Context, orderID entity.OrderID, reason usecase.RefundReason, now time.Time) (*entity.Order, error) {
	if s.refundOrderFn != nil {
		return s.refundOrderFn(ctx, orderID, reason, now)
	}
	return &entity.Order{ID: orderID, Status: entity.OrderStatusRefunded}, nil
}

// newWebhookSvc constructs the real StripeWebhookService with the given deps.
// settlementRepo is no longer a constructor parameter (removed in Fix #1 — it
// was previously injected but never used).
func newWebhookSvc(
	processedRepo usecase.ProcessedWebhookEventRepository,
	orderRepo entity.OrderRepository,
	refundUC usecase.OrderRefunder,
	t *testing.T,
) usecase.StripeWebhookService {
	return usecase.NewStripeWebhookService(processedRepo, orderRepo, refundUC, &stubFulfiller{}, newTestLogger(t))
}

// stubFulfiller records the charges it was asked to fulfill.
type stubFulfiller struct {
	refs []string
	err  error
}

func (s *stubFulfiller) FulfillPayment(_ context.Context, cardAuthorizationID string) error {
	s.refs = append(s.refs, cardAuthorizationID)
	return s.err
}

// ─────────────────────────────────────────────────────────────────────────────
// Duplicate event — applied at most once (idempotency)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DuplicateEvent_AppliedOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	refundCallCount := 0
	markProcessedCallCount := 0

	processedRepo := &stubProcessedWebhookEventRepo{
		isProcessedFn: func() func(context.Context, string) (bool, error) {
			call := 0
			return func(_ context.Context, _ string) (bool, error) {
				call++
				return call > 1, nil
			}
		}(),
		markProcessedFn: func(_ context.Context, _ string) error {
			markProcessedCallCount++
			return nil
		},
	}

	// The order repo resolves pi_ → Order for the dispute path.
	const (
		providerPaymentID = "pi_dup"
		orderID           = entity.OrderID("order-dup")
	)
	orderRepo := &stubOrderRepo{
		getByProviderPaymentIDFn: func(_ context.Context, _ string) (*entity.Order, error) {
			return &entity.Order{ID: orderID, Status: entity.OrderStatusPaid,
				Payment: entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: providerPaymentID}}, nil
		},
	}

	refundUC := &stubRefundUC{
		refundOrderFn: func(_ context.Context, _ entity.OrderID, _ usecase.RefundReason, _ time.Time) (*entity.Order, error) {
			refundCallCount++
			return &entity.Order{Status: entity.OrderStatusRefunded}, nil
		},
	}

	svc := newWebhookSvc(processedRepo, orderRepo, refundUC, t)

	event := usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_dup_001",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  "ch_dup",
		ProviderPaymentID: providerPaymentID, // real production path: pi_ used to resolve Order
		ReceivedAt:        time.Now(),
	}

	// First delivery — should process and mark.
	require.NoError(t, svc.HandleEvent(ctx, event))
	assert.Equal(t, 1, refundCallCount, "refund should be called on first delivery")
	assert.Equal(t, 1, markProcessedCallCount, "event should be marked processed after first delivery")

	// Second delivery — should be a no-op (idempotency fast-path).
	require.NoError(t, svc.HandleEvent(ctx, event))
	assert.Equal(t, 1, refundCallCount, "refund must NOT be called again on duplicate delivery")
	assert.Equal(t, 1, markProcessedCallCount, "MarkProcessed must NOT be called again")
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispute event — resolved via ProviderPaymentID → Order → RefundOrder(DISPUTE)
//
// Fix #1: the real production path. Previously OrderID was pre-set in the event
// (which the HTTP handler could never do). Now the handler extracts ProviderChargeID +
// ProviderPaymentID; the service calls GetByProviderPaymentID to resolve the Order.
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_ResolvesViaProviderPaymentID(t *testing.T) {
	// @spec components/adapter/fan/api/webhook/payment-events "Dispute on a platform order"
	t.Parallel()

	ctx := context.Background()

	const (
		providerPaymentID = "pi_dispute_wh"
		providerChargeID  = "ch_dispute_wh"
		orderID           = entity.OrderID("order-dispute-wh")
	)

	// The order repo is the real resolution path: pi_ → Order.
	orderRepo := &stubOrderRepo{
		getByProviderPaymentIDFn: func(_ context.Context, ref string) (*entity.Order, error) {
			assert.Equal(t, providerPaymentID, ref)
			return &entity.Order{
				ID:     orderID,
				Status: entity.OrderStatusPaid,
				Payment: entity.Payment{
					Provider:          entity.PaymentProviderStripe,
					ProviderPaymentID: providerPaymentID,
				},
			}, nil
		},
	}

	var (
		gotOrderID entity.OrderID
		gotReason  usecase.RefundReason
	)
	refundUC := &stubRefundUC{
		refundOrderFn: func(_ context.Context, id entity.OrderID, reason usecase.RefundReason, _ time.Time) (*entity.Order, error) {
			gotOrderID = id
			gotReason = reason
			return &entity.Order{ID: id, Status: entity.OrderStatusRefunded}, nil
		},
	}

	svc := newWebhookSvc(&stubProcessedWebhookEventRepo{}, orderRepo, refundUC, t)

	require.NoError(t, svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_dispute_001",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  providerChargeID,
		ProviderPaymentID: providerPaymentID,
		ReceivedAt:        time.Now(),
	}))

	assert.Equal(t, orderID, gotOrderID, "RefundOrder must be called with the resolved OrderID")
	assert.Equal(t, usecase.RefundReasonDispute, gotReason)
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispute with no ProviderPaymentID — logs warning, returns nil (no retry)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_NoProviderPaymentID_NoError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	refundCalled := false
	refundUC := &stubRefundUC{
		refundOrderFn: func(_ context.Context, _ entity.OrderID, _ usecase.RefundReason, _ time.Time) (*entity.Order, error) {
			refundCalled = true
			return nil, nil
		},
	}

	svc := newWebhookSvc(&stubProcessedWebhookEventRepo{}, &stubOrderRepo{}, refundUC, t)

	// No ProviderPaymentID — cannot resolve Order; handler warns and accepts.
	err := svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_dispute_no_pi",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  "ch_no_pi",
		ProviderPaymentID: "", // missing
		ReceivedAt:        time.Now(),
	})
	require.NoError(t, err)
	assert.False(t, refundCalled, "Refund must NOT be called when ProviderPaymentID is missing")
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispute for payment intent with no matching Order — accept without retry
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_OrderNotFound_NoError(t *testing.T) {
	// @spec components/adapter/fan/api/webhook/payment-events "Dispute on an unknown payment"
	t.Parallel()

	ctx := context.Background()

	orderRepo := &stubOrderRepo{
		getByProviderPaymentIDFn: func(_ context.Context, _ string) (*entity.Order, error) {
			return nil, apperr.ErrNotFound
		},
	}

	refundCalled := false
	refundUC := &stubRefundUC{
		refundOrderFn: func(_ context.Context, _ entity.OrderID, _ usecase.RefundReason, _ time.Time) (*entity.Order, error) {
			refundCalled = true
			return nil, nil
		},
	}

	svc := newWebhookSvc(&stubProcessedWebhookEventRepo{}, orderRepo, refundUC, t)

	err := svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_dispute_notfound",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  "ch_notfound",
		ProviderPaymentID: "pi_notfound",
		ReceivedAt:        time.Now(),
	})
	require.NoError(t, err)
	assert.False(t, refundCalled, "Refund must NOT be called when Order cannot be found")
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispute for an Order an admin already refunded — acknowledged, stays Refunded
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_AlreadyRefunded_Acknowledged(t *testing.T) {
	// @spec components/adapter/fan/api/webhook/payment-events "Refund already applied"
	t.Parallel()

	const orderID = entity.OrderID("order-already-refunded")
	orderRepo := &stubOrderRepo{
		getByProviderPaymentIDFn: func(_ context.Context, _ string) (*entity.Order, error) {
			return &entity.Order{ID: orderID, Status: entity.OrderStatusRefunded}, nil
		},
	}
	var got *entity.Order
	refundUC := &stubRefundUC{
		// RefundOrder is idempotent: an already refunded Order comes back as is.
		refundOrderFn: func(_ context.Context, id entity.OrderID, _ usecase.RefundReason, _ time.Time) (*entity.Order, error) {
			got = &entity.Order{ID: id, Status: entity.OrderStatusRefunded}
			return got, nil
		},
	}

	svc := newWebhookSvc(&stubProcessedWebhookEventRepo{}, orderRepo, refundUC, t)

	err := svc.HandleEvent(context.Background(), usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_dispute_after_refund",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  "ch_after_refund",
		ProviderPaymentID: "pi_after_refund",
		ReceivedAt:        time.Now(),
	})

	require.NoError(t, err, "the notice is acknowledged")
	require.NotNil(t, got)
	assert.Equal(t, entity.OrderStatusRefunded, got.Status, "the Order stays Refunded")
}

// ─────────────────────────────────────────────────────────────────────────────
// charge.refunded — informational; no RefundOrder call
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_ChargeRefunded_Informational(t *testing.T) {
	t.Parallel()
	// @spec components/adapter/fan/api/webhook/payment-events "Refund confirmation"

	ctx := context.Background()

	refundCalled := false
	refundUC := &stubRefundUC{
		refundOrderFn: func(_ context.Context, _ entity.OrderID, _ usecase.RefundReason, _ time.Time) (*entity.Order, error) {
			refundCalled = true
			return nil, nil
		},
	}

	svc := newWebhookSvc(&stubProcessedWebhookEventRepo{}, &stubOrderRepo{}, refundUC, t)

	err := svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:  "evt_charge_refunded_001",
		Type:             usecase.StripeEventChargeRefunded,
		ProviderChargeID: "ch_refunded",
		ReceivedAt:       time.Now(),
	})
	require.NoError(t, err)
	assert.False(t, refundCalled, "Refund must NOT be called for informational charge.refunded")
}

// ─────────────────────────────────────────────────────────────────────────────
// Unknown event type — accepted with no error (Stripe must not retry)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_UnknownEventType_AcceptedWithNoError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	svc := newWebhookSvc(&stubProcessedWebhookEventRepo{}, &stubOrderRepo{}, &stubRefundUC{}, t)

	err := svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID: "evt_unknown_001",
		Type:            usecase.StripeEventType("customer.created"),
		ReceivedAt:      time.Now(),
	})
	require.NoError(t, err, "unknown event types must be accepted without error")
}

// ─────────────────────────────────────────────────────────────────────────────
// IsProcessed DB error — propagated (prevents marking processed)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_IsProcessedDBError_Propagated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	processedRepo := &stubProcessedWebhookEventRepo{
		isProcessedFn: func(_ context.Context, _ string) (bool, error) {
			return false, apperr.New(apperr.ErrInternal.Code, "db failure")
		},
	}

	svc := newWebhookSvc(processedRepo, &stubOrderRepo{}, &stubRefundUC{}, t)

	err := svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_db_err",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderPaymentID: "pi_db_err",
		ReceivedAt:        time.Now(),
	})
	require.Error(t, err, "DB error from IsProcessed must be propagated so Stripe retries")
}

// ─────────────────────────────────────────────────────────────────────────────
// Fix #4: MarkProcessed failure propagates → 500 so Stripe retries
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_MarkProcessedFailure_Propagated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const (
		providerPaymentID = "pi_mark_fail"
		orderID           = entity.OrderID("order-mark-fail")
	)

	orderRepo := &stubOrderRepo{
		getByProviderPaymentIDFn: func(_ context.Context, _ string) (*entity.Order, error) {
			return &entity.Order{ID: orderID, Status: entity.OrderStatusPaid,
				Payment: entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: providerPaymentID}}, nil
		},
	}

	processedRepo := &stubProcessedWebhookEventRepo{
		isProcessedFn: func(_ context.Context, _ string) (bool, error) { return false, nil },
		markProcessedFn: func(_ context.Context, _ string) error {
			// Simulate a transient DB failure when recording the processed event.
			return apperr.New(apperr.ErrInternal.Code, "mark processed failed")
		},
	}

	svc := newWebhookSvc(processedRepo, orderRepo, &stubRefundUC{}, t)

	err := svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_mark_fail",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  "ch_mark_fail",
		ProviderPaymentID: providerPaymentID,
		ReceivedAt:        time.Now(),
	})
	// Fix #4: error must propagate so handler returns 500 → Stripe retries.
	require.Error(t, err, "MarkProcessed failure must propagate so Stripe retries")
}

func TestStripeWebhook_PaymentIntentSucceeded(t *testing.T) {
	t.Parallel()
	event := usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_pi_1",
		Type:              usecase.StripeEventPaymentIntentSucceeded,
		ProviderPaymentID: "pi_1",
		ReceivedAt:        time.Now(),
	}

	t.Run("checkout charge completes", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Checkout charge completes"
		processed := &stubProcessedWebhookEventRepo{}
		fulfiller := &stubFulfiller{}
		svc := usecase.NewStripeWebhookService(processed, &stubOrderRepo{}, &stubRefundUC{}, fulfiller, newTestLogger(t))

		require.NoError(t, svc.HandleEvent(context.Background(), event))

		assert.Equal(t, []string{"pi_1"}, fulfiller.refs)
	})

	t.Run("fulfillment fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Fulfillment fails"
		marked := 0
		processed := &stubProcessedWebhookEventRepo{markProcessedFn: func(context.Context, string) error { marked++; return nil }}
		fulfiller := &stubFulfiller{err: apperr.New(apperr.ErrUnavailable.Code, "nats down")}
		svc := usecase.NewStripeWebhookService(processed, &stubOrderRepo{}, &stubRefundUC{}, fulfiller, newTestLogger(t))

		err := svc.HandleEvent(context.Background(), event)

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Zero(t, marked, "the notice is not counted as applied, so a redelivery fulfills it")
	})
}
