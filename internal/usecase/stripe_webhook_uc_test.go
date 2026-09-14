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

func (s *stubRefundUC) RefundOrder(ctx context.Context, orderID entity.OrderID, reason usecase.RefundReason, now time.Time) (*entity.Order, error) {
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
	refundUC usecase.RefundOrderUseCase,
	t *testing.T,
) usecase.StripeWebhookService {
	return usecase.NewStripeWebhookService(processedRepo, orderRepo, refundUC, newTestLogger(t))
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
		piRef   = "pi_dup"
		orderID = entity.OrderID("order-dup")
	)
	orderRepo := &stubOrderRepo{
		getByPaymentIntentRefFn: func(_ context.Context, _ string) (*entity.Order, error) {
			return &entity.Order{ID: orderID, Status: entity.OrderStatusPaid,
				Payment: entity.Payment{Provider: entity.PaymentProviderStripe, PaymentIntentRef: piRef}}, nil
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
		ProviderEventID:  "evt_dup_001",
		Type:             usecase.StripeEventChargeDisputeCreated,
		ChargeRef:        "ch_dup",
		PaymentIntentRef: piRef, // real production path: pi_ used to resolve Order
		ReceivedAt:       time.Now(),
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
// Dispute event — resolved via PaymentIntentRef → Order → RefundOrder(DISPUTE)
//
// Fix #1: the real production path. Previously OrderID was pre-set in the event
// (which the HTTP handler could never do). Now the handler extracts ChargeRef +
// PaymentIntentRef; the service calls GetByPaymentIntentRef to resolve the Order.
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_ResolvesViaPaymentIntentRef(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const (
		piRef   = "pi_dispute_wh"
		chRef   = "ch_dispute_wh"
		orderID = entity.OrderID("order-dispute-wh")
	)

	// The order repo is the real resolution path: pi_ → Order.
	orderRepo := &stubOrderRepo{
		getByPaymentIntentRefFn: func(_ context.Context, ref string) (*entity.Order, error) {
			assert.Equal(t, piRef, ref)
			return &entity.Order{
				ID:     orderID,
				Status: entity.OrderStatusPaid,
				Payment: entity.Payment{
					Provider:         entity.PaymentProviderStripe,
					PaymentIntentRef: piRef,
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
		ProviderEventID:  "evt_dispute_001",
		Type:             usecase.StripeEventChargeDisputeCreated,
		ChargeRef:        chRef,
		PaymentIntentRef: piRef,
		ReceivedAt:       time.Now(),
	}))

	assert.Equal(t, orderID, gotOrderID, "RefundOrder must be called with the resolved OrderID")
	assert.Equal(t, usecase.RefundReasonDispute, gotReason)
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispute with no PaymentIntentRef — logs warning, returns nil (no retry)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_NoPaymentIntentRef_NoError(t *testing.T) {
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

	// No PaymentIntentRef — cannot resolve Order; handler warns and accepts.
	err := svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:  "evt_dispute_no_pi",
		Type:             usecase.StripeEventChargeDisputeCreated,
		ChargeRef:        "ch_no_pi",
		PaymentIntentRef: "", // missing
		ReceivedAt:       time.Now(),
	})
	require.NoError(t, err)
	assert.False(t, refundCalled, "RefundOrder must NOT be called when PaymentIntentRef is missing")
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispute for payment intent with no matching Order — accept without retry
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_OrderNotFound_NoError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	orderRepo := &stubOrderRepo{
		getByPaymentIntentRefFn: func(_ context.Context, _ string) (*entity.Order, error) {
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
		ProviderEventID:  "evt_dispute_notfound",
		Type:             usecase.StripeEventChargeDisputeCreated,
		ChargeRef:        "ch_notfound",
		PaymentIntentRef: "pi_notfound",
		ReceivedAt:       time.Now(),
	})
	require.NoError(t, err)
	assert.False(t, refundCalled, "RefundOrder must NOT be called when Order cannot be found")
}

// ─────────────────────────────────────────────────────────────────────────────
// charge.refunded — informational; no RefundOrder call
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_ChargeRefunded_Informational(t *testing.T) {
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

	err := svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID: "evt_charge_refunded_001",
		Type:            usecase.StripeEventChargeRefunded,
		ChargeRef:       "ch_refunded",
		ReceivedAt:      time.Now(),
	})
	require.NoError(t, err)
	assert.False(t, refundCalled, "RefundOrder must NOT be called for informational charge.refunded")
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
		ProviderEventID:  "evt_db_err",
		Type:             usecase.StripeEventChargeDisputeCreated,
		PaymentIntentRef: "pi_db_err",
		ReceivedAt:       time.Now(),
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
		piRef   = "pi_mark_fail"
		orderID = entity.OrderID("order-mark-fail")
	)

	orderRepo := &stubOrderRepo{
		getByPaymentIntentRefFn: func(_ context.Context, _ string) (*entity.Order, error) {
			return &entity.Order{ID: orderID, Status: entity.OrderStatusPaid,
				Payment: entity.Payment{Provider: entity.PaymentProviderStripe, PaymentIntentRef: piRef}}, nil
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
		ProviderEventID:  "evt_mark_fail",
		Type:             usecase.StripeEventChargeDisputeCreated,
		ChargeRef:        "ch_mark_fail",
		PaymentIntentRef: piRef,
		ReceivedAt:       time.Now(),
	})
	// Fix #4: error must propagate so handler returns 500 → Stripe retries.
	require.Error(t, err, "MarkProcessed failure must propagate so Stripe retries")
}
