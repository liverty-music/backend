package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// webhookFixture holds the mocks of a StripeWebhookService under test.
type webhookFixture struct {
	processed *ucmocks.MockProcessedWebhookEventRepository
	orders    *entitymocks.MockOrderRepository
	refunds   *ucmocks.MockOrderRefunder
	cards     *entitymocks.MockCardAuthorizationRepository
	lottery   *ucmocks.MockLotteryEntryUseCase
	resale    *ucmocks.MockResaleEntryUseCase
	svc       usecase.StripeWebhookService
}

func newWebhookFixture(t *testing.T) *webhookFixture {
	t.Helper()
	f := &webhookFixture{
		processed: ucmocks.NewMockProcessedWebhookEventRepository(t),
		orders:    entitymocks.NewMockOrderRepository(t),
		refunds:   ucmocks.NewMockOrderRefunder(t),
		cards:     entitymocks.NewMockCardAuthorizationRepository(t),
		lottery:   ucmocks.NewMockLotteryEntryUseCase(t),
		resale:    ucmocks.NewMockResaleEntryUseCase(t),
	}
	f.svc = usecase.NewStripeWebhookService(f.processed, f.orders, f.refunds, f.cards, f.lottery, f.resale, newTestLogger(t))
	return f
}

// firstDelivery makes the notice providerEventID unprocessed and expects it
// to be recorded as processed once.
func (f *webhookFixture) firstDelivery(providerEventID string) {
	f.processed.EXPECT().IsProcessed(mock.Anything, providerEventID).Return(false, nil).Once()
	f.processed.EXPECT().MarkProcessed(mock.Anything, providerEventID).Return(nil).Once()
}

// paidOrderOf makes providerPaymentID resolve to the Paid Order orderID.
func (f *webhookFixture) paidOrderOf(providerPaymentID string, orderID entity.OrderID) {
	f.orders.EXPECT().GetByProviderPaymentID(mock.Anything, providerPaymentID).Return(&entity.Order{
		ID: orderID, Status: entity.OrderStatusPaid,
		Payment: entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: providerPaymentID},
	}, nil)
}

// ─────────────────────────────────────────────────────────────────────────────
// Duplicate event — applied at most once (idempotency)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DuplicateEvent_AppliedOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// The order repo resolves pi_ → Order for the dispute path.
	const (
		providerPaymentID = "pi_dup"
		orderID           = entity.OrderID("order-dup")
	)
	f := newWebhookFixture(t)
	// First delivery: processed and marked once. Second delivery: already
	// processed, so neither Refund nor MarkProcessed runs again.
	f.firstDelivery("evt_dup_001")
	f.processed.EXPECT().IsProcessed(mock.Anything, "evt_dup_001").Return(true, nil).Once()
	f.paidOrderOf(providerPaymentID, orderID)
	f.refunds.EXPECT().Refund(mock.Anything, orderID, usecase.RefundReasonDispute, mock.Anything).
		Return(&entity.Order{Status: entity.OrderStatusRefunded}, nil).Once()

	event := usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_dup_001",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  "ch_dup",
		ProviderPaymentID: providerPaymentID, // real production path: pi_ used to resolve Order
		ReceivedAt:        time.Now(),
	}

	// First delivery — should process and mark.
	require.NoError(t, f.svc.HandleEvent(ctx, event))
	f.refunds.AssertNumberOfCalls(t, "Refund", 1)
	f.processed.AssertNumberOfCalls(t, "MarkProcessed", 1)

	// Second delivery — should be a no-op (idempotency fast-path).
	require.NoError(t, f.svc.HandleEvent(ctx, event))
	f.refunds.AssertNumberOfCalls(t, "Refund", 1)
	f.processed.AssertNumberOfCalls(t, "MarkProcessed", 1)
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

	f := newWebhookFixture(t)
	f.firstDelivery("evt_dispute_001")
	// The order repo is the real resolution path: pi_ → Order.
	f.paidOrderOf(providerPaymentID, orderID)
	// RefundOrder must be called with the resolved OrderID, for a dispute.
	f.refunds.EXPECT().Refund(mock.Anything, orderID, usecase.RefundReasonDispute, mock.Anything).
		Return(&entity.Order{ID: orderID, Status: entity.OrderStatusRefunded}, nil).Once()

	require.NoError(t, f.svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_dispute_001",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  providerChargeID,
		ProviderPaymentID: providerPaymentID,
		ReceivedAt:        time.Now(),
	}))
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispute with no ProviderPaymentID — logs warning, returns nil (no retry)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_NoProviderPaymentID_NoError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	f := newWebhookFixture(t)
	f.firstDelivery("evt_dispute_no_pi")

	// No ProviderPaymentID — cannot resolve Order; handler warns and accepts.
	err := f.svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_dispute_no_pi",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  "ch_no_pi",
		ProviderPaymentID: "", // missing
		ReceivedAt:        time.Now(),
	})
	require.NoError(t, err)
	f.refunds.AssertNotCalled(t, "Refund", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispute for payment intent with no matching Order — accept without retry
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_OrderNotFound_NoError(t *testing.T) {
	// @spec components/adapter/fan/api/webhook/payment-events "Dispute on an unknown payment"
	t.Parallel()

	ctx := context.Background()

	f := newWebhookFixture(t)
	f.firstDelivery("evt_dispute_notfound")
	f.orders.EXPECT().GetByProviderPaymentID(mock.Anything, "pi_notfound").Return(nil, apperr.ErrNotFound)

	err := f.svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_dispute_notfound",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderChargeID:  "ch_notfound",
		ProviderPaymentID: "pi_notfound",
		ReceivedAt:        time.Now(),
	})
	require.NoError(t, err)
	f.refunds.AssertNotCalled(t, "Refund", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispute for an Order an admin already refunded — acknowledged, stays Refunded
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_DisputeCreated_AlreadyRefunded_Acknowledged(t *testing.T) {
	// @spec components/adapter/fan/api/webhook/payment-events "Refund already applied"
	t.Parallel()

	const orderID = entity.OrderID("order-already-refunded")
	f := newWebhookFixture(t)
	f.firstDelivery("evt_dispute_after_refund")
	f.orders.EXPECT().GetByProviderPaymentID(mock.Anything, "pi_after_refund").
		Return(&entity.Order{ID: orderID, Status: entity.OrderStatusRefunded}, nil)
	var got *entity.Order
	// RefundOrder is idempotent: an already refunded Order comes back as is.
	f.refunds.EXPECT().Refund(mock.Anything, orderID, usecase.RefundReasonDispute, mock.Anything).
		RunAndReturn(func(_ context.Context, id entity.OrderID, _ usecase.RefundReason, _ time.Time) (*entity.Order, error) {
			got = &entity.Order{ID: id, Status: entity.OrderStatusRefunded}
			return got, nil
		}).Once()

	err := f.svc.HandleEvent(context.Background(), usecase.StripeWebhookEvent{
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

	f := newWebhookFixture(t)
	f.firstDelivery("evt_charge_refunded_001")

	err := f.svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:  "evt_charge_refunded_001",
		Type:             usecase.StripeEventChargeRefunded,
		ProviderChargeID: "ch_refunded",
		ReceivedAt:       time.Now(),
	})
	require.NoError(t, err)
	f.refunds.AssertNotCalled(t, "Refund", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// ─────────────────────────────────────────────────────────────────────────────
// Unknown event type — accepted with no error (Stripe must not retry)
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_UnknownEventType_AcceptedWithNoError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	f := newWebhookFixture(t)
	f.firstDelivery("evt_unknown_001")

	err := f.svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
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

	f := newWebhookFixture(t)
	f.processed.EXPECT().IsProcessed(mock.Anything, "evt_db_err").
		Return(false, apperr.New(apperr.ErrInternal.Code, "db failure")).Once()

	err := f.svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_db_err",
		Type:              usecase.StripeEventChargeDisputeCreated,
		ProviderPaymentID: "pi_db_err",
		ReceivedAt:        time.Now(),
	})
	require.Error(t, err, "DB error from IsProcessed must be propagated so Stripe retries")
	f.processed.AssertNotCalled(t, "MarkProcessed", mock.Anything, mock.Anything)
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

	f := newWebhookFixture(t)
	f.paidOrderOf(providerPaymentID, orderID)
	f.refunds.EXPECT().Refund(mock.Anything, orderID, usecase.RefundReasonDispute, mock.Anything).
		Return(&entity.Order{ID: orderID, Status: entity.OrderStatusRefunded}, nil).Once()
	f.processed.EXPECT().IsProcessed(mock.Anything, "evt_mark_fail").Return(false, nil).Once()
	// Simulate a transient DB failure when recording the processed event.
	f.processed.EXPECT().MarkProcessed(mock.Anything, "evt_mark_fail").
		Return(apperr.New(apperr.ErrInternal.Code, "mark processed failed")).Once()

	err := f.svc.HandleEvent(ctx, usecase.StripeWebhookEvent{
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

	t.Run("completed charge", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Completed charge"
		// No Order is read or refunded: OrderUseCase.Issue made the Order
		// Paid, and Order.Issue announced it. The order and refund mocks
		// expect no call.
		f := newWebhookFixture(t)
		// The notice is recorded as processed, once.
		f.firstDelivery("evt_pi_1")

		require.NoError(t, f.svc.HandleEvent(context.Background(), event))
	})

	t.Run("redelivered", func(t *testing.T) {
		t.Parallel()
		f := newWebhookFixture(t)
		f.processed.EXPECT().IsProcessed(mock.Anything, "evt_pi_1").Return(true, nil).Once()

		require.NoError(t, f.svc.HandleEvent(context.Background(), event))

		f.processed.AssertNotCalled(t, "MarkProcessed", mock.Anything, mock.Anything)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// A card authorization became Authorized: made into the entry it pays for
// ─────────────────────────────────────────────────────────────────────────────

func TestStripeWebhook_CardAuthorizationAuthorized(t *testing.T) {
	t.Parallel()

	const (
		piID = "pi_authorized"
		caID = "019a0000-0000-7000-8000-000000000001"
	)
	event := func(eventID string) usecase.StripeWebhookEvent {
		return usecase.StripeWebhookEvent{ProviderEventID: eventID, Type: usecase.StripeEventPaymentIntentAmountCapturableUpdated, ProviderPaymentID: piID}
	}
	card := func(purpose entity.CardAuthorizationPurpose) *entity.CardAuthorization {
		return &entity.CardAuthorization{ID: caID, ProviderID: piID, Status: entity.CardAuthorizationStatusAuthorized,
			Purchase: entity.CardAuthorizationPurchase{Purpose: purpose}}
	}
	noUser := (*entity.UserID)(nil)

	t.Run("lottery entry, browser closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Fan closed the browser"
		f := newWebhookFixture(t)
		f.firstDelivery("evt_1")
		f.cards.EXPECT().GetByProviderCardAuthorizationID(mock.Anything, piID).Return(card(entity.CardAuthorizationPurposeLotteryEntry), nil)
		f.lottery.EXPECT().Confirm(mock.Anything, caID, noUser).Return(&entity.LotteryEntry{ID: "entry-1"}, nil).Once()

		require.NoError(t, f.svc.HandleEvent(context.Background(), event("evt_1")))
	})

	t.Run("resale entry, browser closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Resale application, browser closed"
		f := newWebhookFixture(t)
		f.firstDelivery("evt_2")
		f.cards.EXPECT().GetByProviderCardAuthorizationID(mock.Anything, piID).Return(card(entity.CardAuthorizationPurposeResaleEntry), nil)
		f.resale.EXPECT().Confirm(mock.Anything, caID, noUser).
			Return(&usecase.ResaleEntryView{Entry: &entity.ResaleEntry{ID: "resale-1"}}, nil).Once()

		require.NoError(t, f.svc.HandleEvent(context.Background(), event("evt_2")))
	})

	t.Run("checkout or unknown purpose acknowledged", func(t *testing.T) {
		t.Parallel()
		for _, purpose := range []entity.CardAuthorizationPurpose{entity.CardAuthorizationPurposeCheckout, entity.CardAuthorizationPurposeUnspecified} {
			f := newWebhookFixture(t)
			f.firstDelivery("evt_3")
			f.cards.EXPECT().GetByProviderCardAuthorizationID(mock.Anything, piID).Return(card(purpose), nil)

			require.NoError(t, f.svc.HandleEvent(context.Background(), event("evt_3")), purpose.String())
		}
	})

	t.Run("card authorization the platform did not open", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Card authorization the platform did not open"
		f := newWebhookFixture(t)
		f.firstDelivery("evt_4")
		f.cards.EXPECT().GetByProviderCardAuthorizationID(mock.Anything, piID).Return(nil, apperr.New(codes.NotFound, "unknown"))

		require.NoError(t, f.svc.HandleEvent(context.Background(), event("evt_4")))
	})

	t.Run("notice without a PaymentIntent id skipped", func(t *testing.T) {
		t.Parallel()
		f := newWebhookFixture(t)
		f.firstDelivery("evt_5")

		require.NoError(t, f.svc.HandleEvent(context.Background(), usecase.StripeWebhookEvent{
			ProviderEventID: "evt_5", Type: usecase.StripeEventPaymentIntentAmountCapturableUpdated,
		}))
	})

	t.Run("rejection acknowledged", func(t *testing.T) {
		t.Parallel()
		for _, code := range []codes.Code{codes.PermissionDenied, codes.FailedPrecondition, codes.InvalidArgument, codes.AlreadyExists} {
			f := newWebhookFixture(t)
			f.firstDelivery("evt_6")
			f.cards.EXPECT().GetByProviderCardAuthorizationID(mock.Anything, piID).Return(card(entity.CardAuthorizationPurposeLotteryEntry), nil)
			f.lottery.EXPECT().Confirm(mock.Anything, caID, noUser).Return(nil, apperr.New(code, "rejected")).Once()

			require.NoError(t, f.svc.HandleEvent(context.Background(), event("evt_6")), code.String())
		}
	})

	t.Run("payment provider unreachable", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/webhook/payment-events "Payment provider unreachable"
		f := newWebhookFixture(t)
		// The notice is not recorded as processed: no MarkProcessed.
		f.processed.EXPECT().IsProcessed(mock.Anything, "evt_7").Return(false, nil).Once()
		f.cards.EXPECT().GetByProviderCardAuthorizationID(mock.Anything, piID).Return(card(entity.CardAuthorizationPurposeLotteryEntry), nil)
		f.lottery.EXPECT().Confirm(mock.Anything, caID, noUser).Return(nil, apperr.New(codes.Unavailable, "stripe unreachable")).Once()

		err := f.svc.HandleEvent(context.Background(), event("evt_7"))

		assert.ErrorIs(t, err, apperr.ErrInternal, "the provider delivers the notice again")
	})
}
