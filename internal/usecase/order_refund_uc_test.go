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
)

// ─────────────────────────────────────────────────────────────────────────────
// Fixture
// ─────────────────────────────────────────────────────────────────────────────

// refundFixture holds the mocks of OrderUseCase.Refund under test. A mock
// with no expectation for a call fails the test when the call happens, so a
// refund, a payout reversal or a commit no test expects is a failure.
type refundFixture struct {
	orders      *entitymocks.MockOrderRepository
	refunds     *entitymocks.MockRefundRepository
	settlements *entitymocks.MockSettlementRepository
	events      *entitymocks.MockEventRepository
	tickets     *entitymocks.MockTicketRepository
	uc          usecase.OrderUseCase
}

func newRefundFixture(t *testing.T) *refundFixture {
	t.Helper()
	f := &refundFixture{
		orders:      entitymocks.NewMockOrderRepository(t),
		refunds:     entitymocks.NewMockRefundRepository(t),
		settlements: entitymocks.NewMockSettlementRepository(t),
		events:      entitymocks.NewMockEventRepository(t),
		tickets:     entitymocks.NewMockTicketRepository(t),
	}
	f.uc = usecase.NewOrderUseCase(usecase.OrderDeps{
		EventRepo:      f.events,
		TicketRepo:     f.tickets,
		OrderRepo:      f.orders,
		RefundRepo:     f.refunds,
		SettlementRepo: f.settlements,
		Logger:         newTestLogger(t),
	})
	return f
}

func paidOrder(id entity.OrderID, amount int64, providerPaymentID string, paidAt time.Time) *entity.Order {
	return &entity.Order{
		ID:       id,
		UserID:   entity.UserID("user-" + string(id)),
		Status:   entity.OrderStatusPaid,
		Amount:   amount,
		Currency: "JPY",
		PayTime:  &paidAt,
		Payment: entity.Payment{
			Provider:          entity.PaymentProviderStripe,
			ProviderPaymentID: providerPaymentID,
		},
	}
}

// reads makes the first read of order's id return order.
func (f *refundFixture) reads(order *entity.Order) {
	f.orders.EXPECT().Get(mock.Anything, order.ID).Return(order, nil).Once()
}

// eventIn makes the Event "event-<order id>" of orderID be in state.
func (f *refundFixture) eventIn(orderID entity.OrderID, state entity.PublishState) {
	eventID := "event-" + string(orderID)
	f.orders.EXPECT().GetEventID(mock.Anything, orderID).Return(eventID, nil).Once()
	f.events.EXPECT().Get(mock.Anything, eventID).Return(&entity.Event{ID: eventID, PublishState: &state}, nil).Once()
}

// cancelledEvent makes the Event of orderID CANCELLED.
func (f *refundFixture) cancelledEvent(orderID entity.OrderID) {
	f.eventIn(orderID, entity.PublishStateCancelled)
}

// holdsTickets makes order's user hold issued Issued and voided Voided
// tickets of order, and one Issued ticket of another order, which is never
// counted.
func (f *refundFixture) holdsTickets(order *entity.Order, issued, voided int) {
	var out []*entity.Ticket
	for range issued {
		out = append(out, &entity.Ticket{OrderID: order.ID, UserID: order.UserID, Status: entity.TicketStatusIssued})
	}
	for range voided {
		out = append(out, &entity.Ticket{OrderID: order.ID, UserID: order.UserID, Status: entity.TicketStatusVoided})
	}
	out = append(out, &entity.Ticket{OrderID: "other-order", UserID: order.UserID, Status: entity.TicketStatusIssued})
	f.tickets.EXPECT().ListByUser(mock.Anything, order.UserID).Return(out, nil)
}

// settledAs makes s the Settlement of orderID.
func (f *refundFixture) settledAs(orderID entity.OrderID, s *entity.Settlement) {
	f.settlements.EXPECT().GetByOrderID(mock.Anything, orderID).Return(s, nil)
}

// heldSettlement makes the Settlement of orderID Held with one 5000 yen
// split, as Order.Issue stores it.
func (f *refundFixture) heldSettlement(orderID entity.OrderID) {
	f.settledAs(orderID, &entity.Settlement{ID: "settle-" + entity.SettlementID(orderID), OrderID: orderID, Status: entity.SettlementStatusHeld,
		Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 5000}}})
}

// refundsCharge resolves the charge of order's payment to providerChargeID
// and refunds it as providerRefundID, once.
func (f *refundFixture) refundsCharge(order *entity.Order, providerChargeID, providerRefundID string) {
	f.orders.EXPECT().GetProviderChargeID(mock.Anything, order.Payment.ProviderPaymentID).Return(providerChargeID, nil).Once()
	f.orders.EXPECT().CreateRefund(mock.Anything, mock.Anything).Return(providerRefundID, nil).Once()
}

// commits expects one CommitRefund and returns what it was given. The read
// of order that follows a successful commit returns order Refunded with the
// committed refund, as the database does.
func (f *refundFixture) commits(order *entity.Order) *entity.RefundCommit {
	var commit entity.RefundCommit
	f.refunds.EXPECT().CommitRefund(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, c entity.RefundCommit) error {
			commit = c
			return nil
		}).Once()
	f.orders.EXPECT().Get(mock.Anything, order.ID).
		RunAndReturn(func(context.Context, entity.OrderID) (*entity.Order, error) { return refundedWith(order, commit), nil }).Once()
	return &commit
}

// refundedWith returns order Refunded with the refund of commit.
func refundedWith(order *entity.Order, commit entity.RefundCommit) *entity.Order {
	refunded := *order
	refunded.Status = entity.OrderStatusRefunded
	refunded.Refund = &entity.OrderRefund{
		Reason: commit.Reason, Amount: commit.Amount, ProviderRefundID: commit.ProviderRefundID, RefundTime: commit.RefundTime,
	}
	return &refunded
}

// ─────────────────────────────────────────────────────────────────────────────
// CANCELLATION — allowed while Order is Paid and its Event CANCELLED;
// Released settlement
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_Cancellation_HappyPath(t *testing.T) {
	t.Parallel()
	// @spec components/usecase/order/refund "Cancellation refund"
	// @spec components/usecase/order/refund "Payout already released"
	// @spec components/usecase/order/refund "Cancellation refund for a cancelled event"

	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	paidAt := now.Add(-30 * 24 * time.Hour)

	const (
		orderID           = entity.OrderID("order-cancel")
		settleID          = entity.SettlementID("settle-cancel")
		providerPayoutID  = "tr_cancel"
		providerChargeID  = "ch_cancel"
		providerPaymentID = "pi_cancel"
	)

	order := paidOrder(orderID, 10000, providerPaymentID, paidAt)

	f := newRefundFixture(t)
	f.reads(order)
	f.cancelledEvent(orderID)
	f.holdsTickets(order, 2, 0)
	// Settlement is Released (Transfer already paid out) — reversal must occur.
	f.settledAs(orderID, &entity.Settlement{
		ID:               settleID,
		OrderID:          orderID,
		Status:           entity.SettlementStatusReleased,
		ProviderChargeID: providerChargeID,
		Splits: []entity.SettlementSplit{
			{PayeeOrganizerID: "org-1", Amount: 9000, ProviderPayoutID: providerPayoutID},
		},
	})
	// Fix #2: idempotency key must be keyed on OrderID, not SettlementID.
	f.orders.EXPECT().CreateRefund(mock.Anything, entity.RefundParams{
		OrderID: orderID, ProviderChargeID: providerChargeID, Amount: 10000,
	}).Return("re_cancel", nil).Once()
	f.settlements.EXPECT().ReversePayout(mock.Anything, entity.ReversePayoutParams{
		SettlementID: settleID, ProviderPayoutID: providerPayoutID, Amount: 9000,
	}).Return("trr_cancel_reversal", nil).Once()
	// CommitRefund is the single atomic DB boundary — all three state
	// transitions (settlement→Reversed, tickets→Voided, order→Refunded) happen
	// here in production. The test validates that commit is called with the
	// correct SettlementID and that the reversed split's trr_ ref is passed.
	commit := f.commits(order)

	result, err := f.uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)

	assert.Equal(t, orderID, commit.OrderID)
	assert.Equal(t, settleID, commit.SettlementID)
	require.Len(t, commit.ReversedSplits, 1)
	assert.Equal(t, "trr_cancel_reversal", commit.ReversedSplits[0].ProviderPayoutReversalID)
}

// ─────────────────────────────────────────────────────────────────────────────
// Missing settlement — broken data, nothing refunded
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_SettlementUnreadable(t *testing.T) {
	t.Parallel()
	// @spec components/usecase/order/refund "Settlement unreadable"
	now := time.Now()
	const orderID = entity.OrderID("order-no-settle")
	order := paidOrder(orderID, 5000, "pi_no_settle", now.Add(-7*24*time.Hour))

	f := newRefundFixture(t)
	f.reads(order)
	f.settlements.EXPECT().GetByOrderID(mock.Anything, orderID).Return(nil, apperr.New(apperr.ErrNotFound.Code, "no settlement"))
	// No CreateRefund or CommitRefund is expected: no money moves and nothing
	// is recorded.

	_, err := f.uc.Refund(context.Background(), orderID, usecase.RefundReasonDispute, now)

	assert.ErrorIs(t, err, apperr.ErrInternal)
	assert.Equal(t, entity.OrderStatusPaid, order.Status, "the Order stays Paid")
}

// ─────────────────────────────────────────────────────────────────────────────
// Idempotent replay — already-refunded Order is returned unchanged
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_IdempotentReplay_AlreadyRefunded(t *testing.T) {
	t.Parallel()
	// @spec components/usecase/order/refund "Repeated refund"

	ctx := context.Background()
	now := time.Now()

	const orderID = entity.OrderID("order-already-refunded")

	alreadyRefunded := &entity.Order{
		ID:     orderID,
		Status: entity.OrderStatusRefunded,
		Amount: 8000,
		Payment: entity.Payment{
			Provider:          entity.PaymentProviderStripe,
			ProviderPaymentID: "pi_already",
		},
	}

	f := newRefundFixture(t)
	f.reads(alreadyRefunded)

	result, err := f.uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	f.orders.AssertNotCalled(t, "CreateRefund", mock.Anything, mock.Anything)
}

// ─────────────────────────────────────────────────────────────────────────────
// Not-refundable — Pending order returns FAILED_PRECONDITION
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_NotRefundable_PendingOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now()

	const orderID = entity.OrderID("order-pending")

	pendingOrder := &entity.Order{
		ID:     orderID,
		Status: entity.OrderStatusPending,
		Amount: 6000,
		Payment: entity.Payment{
			Provider:          entity.PaymentProviderStripe,
			ProviderPaymentID: "pi_pending",
		},
	}

	f := newRefundFixture(t)
	f.reads(pendingOrder)

	_, err := f.uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "expected FailedPrecondition for a non-Paid order")
}

// ─────────────────────────────────────────────────────────────────────────────
// DISPUTE — chargeback-after-payout reverses the transfer
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_Dispute_AfterPayout(t *testing.T) {
	t.Parallel()
	// @spec components/usecase/order/refund "Dispute"

	ctx := context.Background()
	now := time.Now()
	paidAt := now.Add(-60 * 24 * time.Hour)

	const (
		orderID          = entity.OrderID("order-dispute")
		settleID         = entity.SettlementID("settle-dispute")
		providerPayoutID = "tr_dispute"
		providerChargeID = "ch_dispute"
	)

	order := paidOrder(orderID, 12000, "pi_dispute", paidAt)

	f := newRefundFixture(t)
	f.reads(order)
	f.settledAs(orderID, &entity.Settlement{
		ID:               settleID,
		OrderID:          orderID,
		Status:           entity.SettlementStatusReleased,
		ProviderChargeID: providerChargeID,
		Splits: []entity.SettlementSplit{
			{PayeeOrganizerID: "org-dispute", Amount: 11000, ProviderPayoutID: providerPayoutID},
		},
	})
	// A dispute issues no refund: the card issuer already returned the money.
	// The transfer is reversed for the chargeback-after-payout.
	f.settlements.EXPECT().ReversePayout(mock.Anything, entity.ReversePayoutParams{
		SettlementID: settleID, ProviderPayoutID: providerPayoutID, Amount: 11000,
	}).Return("trr_dispute", nil).Once()
	commit := f.commits(order)

	result, err := f.uc.Refund(ctx, orderID, usecase.RefundReasonDispute, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	// CommitRefund commits atomically, with the reversal.
	assert.Equal(t, orderID, commit.OrderID)
	assert.Equal(t, settleID, commit.SettlementID)
	require.Len(t, commit.ReversedSplits, 1)
	assert.Equal(t, "trr_dispute", commit.ReversedSplits[0].ProviderPayoutReversalID)
}

// ─────────────────────────────────────────────────────────────────────────────
// Event state — a cancellation refund needs a CANCELLED Event; a dispute does not
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_EventState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	const orderID = entity.OrderID("order-on-sale")

	t.Run("a cancellation refund for an event still on sale fails and moves no money", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Cancellation refund for an event still on sale"
		// @spec stories/remove-a-cancelled-concert "Refund before cancellation"
		f := newRefundFixture(t)
		f.reads(paidOrder(orderID, 10000, "pi_on_sale", now.Add(-24*time.Hour)))
		f.eventIn(orderID, entity.PublishStatePublished)
		// No CreateRefund or CommitRefund is expected: no refund and no commit.

		_, err := f.uc.Refund(context.Background(), orderID, usecase.RefundReasonCancellation, now)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("a dispute for an event still on sale is refunded without reading the event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Dispute for an event still on sale"
		order := paidOrder(orderID, 10000, "pi_on_sale", now.Add(-24*time.Hour))
		f := newRefundFixture(t)
		f.reads(order)
		f.heldSettlement(orderID)
		f.commits(order)

		got, err := f.uc.Refund(context.Background(), orderID, usecase.RefundReasonDispute, now)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		f.events.AssertNotCalled(t, "Get", mock.Anything, mock.Anything)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// UNSPECIFIED reason — returns InvalidArgument
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_UnspecifiedReason_InvalidArgument(t *testing.T) {
	t.Parallel()
	// @spec components/usecase/order/refund "Reason missing"

	ctx := context.Background()
	now := time.Now()

	f := newRefundFixture(t)

	_, err := f.uc.Refund(ctx, "order-unspec", usecase.RefundReasonUnspecified, now)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument, "expected InvalidArgument for unspecified reason")
}

// ─────────────────────────────────────────────────────────────────────────────
// Order not found — returns NotFound
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_OrderNotFound(t *testing.T) {
	t.Parallel()
	// @spec components/usecase/order/refund "Unknown order"

	ctx := context.Background()

	f := newRefundFixture(t)
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-missing")).
		Return(nil, apperr.New(apperr.ErrNotFound.Code, "order not found")).Once()

	_, err := f.uc.Refund(ctx, "order-missing", usecase.RefundReasonCancellation, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrNotFound, "expected NotFound")
}

// ─────────────────────────────────────────────────────────────────────────────
// Held settlement — no transfer to reverse; CommitRefund still called to flip
// settlement to Reversed (Fix #6 TOCTOU: blocks payout sweeper from releasing)
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_HeldSettlement_NoReversal_ButCommitCalled(t *testing.T) {
	t.Parallel()
	// @spec components/usecase/order/refund "Payout not yet released"

	ctx := context.Background()
	now := time.Now()
	paidAt := now.Add(-5 * 24 * time.Hour)

	const (
		orderID  = entity.OrderID("order-held")
		settleID = entity.SettlementID("settle-held")
	)

	order := paidOrder(orderID, 8000, "pi_held", paidAt)

	f := newRefundFixture(t)
	f.reads(order)
	f.cancelledEvent(orderID)
	f.holdsTickets(order, 2, 0)
	f.settledAs(orderID, &entity.Settlement{
		ID:               settleID,
		OrderID:          orderID,
		Status:           entity.SettlementStatusHeld, // not yet released
		ProviderChargeID: "ch_held",
		Splits: []entity.SettlementSplit{
			// No ProviderPayoutID — not yet paid out.
			{PayeeOrganizerID: "org-held", Amount: 7000},
		},
	})
	f.orders.EXPECT().CreateRefund(mock.Anything, mock.Anything).Return("re_held", nil).Once()
	commit := f.commits(order)

	result, err := f.uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	// No transfers to reverse (Held = not yet paid out).
	f.settlements.AssertNotCalled(t, "ReversePayout", mock.Anything, mock.Anything)
	// But CommitRefund MUST still be called with the SettlementID to flip the
	// settlement to Reversed (Fix #6: prevents payout sweeper from releasing
	// the refunded order).
	assert.Equal(t, settleID, commit.SettlementID)
	assert.Equal(t, orderID, commit.OrderID)
}

// ─────────────────────────────────────────────────────────────────────────────
// Fix #8: non-Stripe provider returns FailedPrecondition
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_NonStripeProvider_FailedPrecondition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	komojuOrder := &entity.Order{
		ID:     "order-komoju",
		Status: entity.OrderStatusPaid,
		Amount: 5000,
		Payment: entity.Payment{
			Provider:          entity.PaymentProviderKOMOJU,
			ProviderPaymentID: "pi_komoju",
		},
	}

	f := newRefundFixture(t)
	f.reads(komojuOrder)
	f.cancelledEvent(komojuOrder.ID)
	f.heldSettlement(komojuOrder.ID)

	_, err := f.uc.Refund(ctx, "order-komoju", usecase.RefundReasonCancellation, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"non-Stripe provider must return FailedPrecondition (no Stripe refund path)")
}

// ─────────────────────────────────────────────────────────────────────────────
// Fix #8: empty ProviderPaymentID returns FailedPrecondition
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_EmptyProviderPaymentID_FailedPrecondition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	badOrder := &entity.Order{
		ID:     "order-no-pi",
		Status: entity.OrderStatusPaid,
		Amount: 5000,
		Payment: entity.Payment{
			Provider:          entity.PaymentProviderStripe,
			ProviderPaymentID: "", // empty — missing ref
		},
	}

	f := newRefundFixture(t)
	f.reads(badOrder)
	f.cancelledEvent(badOrder.ID)
	f.heldSettlement(badOrder.ID)

	_, err := f.uc.Refund(ctx, "order-no-pi", usecase.RefundReasonCancellation, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"empty ProviderPaymentID must return FailedPrecondition")
}

// ─────────────────────────────────────────────────────────────────────────────
// Fix #3: CommitRefund FailedPrecondition (concurrent refund) is handled
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_ConcurrentRefund_IdempotentViaCommitFailedPrecondition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now()

	const orderID = entity.OrderID("order-concurrent")

	order := paidOrder(orderID, 9000, "pi_concurrent", now.Add(-24*time.Hour))
	// After the concurrent refund the order is Refunded.
	refundedOrder := &entity.Order{ID: orderID, Status: entity.OrderStatusRefunded, Amount: 9000,
		Payment: entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_concurrent"}}

	f := newRefundFixture(t)
	// First read: Paid (pre-refund check).
	f.reads(order)
	f.cancelledEvent(orderID)
	f.holdsTickets(order, 2, 0)
	f.heldSettlement(orderID)
	f.refundsCharge(order, "ch_concurrent", "re_concurrent")
	// CommitRefund returns FailedPrecondition to simulate a concurrent refund
	// that already set the settlement to Reversed between our Stripe calls and
	// our DB commit.
	f.refunds.EXPECT().CommitRefund(mock.Anything, mock.Anything).Return(apperr.ErrFailedPrecondition).Once()
	// The use case must re-read the order on FailedPrecondition to return the
	// post-refund state.
	f.reads(refundedOrder)

	result, err := f.uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	// Returns the current (post-concurrent-refund) state.
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
}

// A FailedPrecondition from CommitRefund is a finished concurrent refund only
// when the Order read again is Refunded. An Order that is not (it was never
// paid, which Order.CommitRefund refuses) fails the Refund instead of being
// returned as if the refund were done.
func TestOrderUseCase_Refund_CommitRefusedForOrderNotRefunded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now()
	const orderID = entity.OrderID("order-not-refunded")
	order := paidOrder(orderID, 9000, "pi_not_refunded", now.Add(-24*time.Hour))

	f := newRefundFixture(t)
	f.reads(order)
	f.cancelledEvent(orderID)
	f.holdsTickets(order, 2, 0)
	f.heldSettlement(orderID)
	f.refundsCharge(order, "ch_not_refunded", "re_not_refunded")
	f.refunds.EXPECT().CommitRefund(mock.Anything, mock.Anything).
		Return(apperr.New(codes.FailedPrecondition, "the order was never paid; no refund is recorded")).Once()
	// The Order is still not Refunded when it is read again after the refused
	// commit.
	f.reads(order)

	result, err := f.uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)

	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	assert.Nil(t, result)
}

// ─────────────────────────────────────────────────────────────────────────────
// CANCELLATION after official resale — only the tickets still Issued
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_AfterResale(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	t.Run("cancellation after a resale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Cancellation after a resale"
		const orderID = entity.OrderID("order-half-resold")
		order := paidOrder(orderID, 10000, "pi_half", now.Add(-30*24*time.Hour))
		f := newRefundFixture(t)
		f.reads(order)
		f.cancelledEvent(orderID)
		f.holdsTickets(order, 1, 1)
		f.settledAs(orderID, &entity.Settlement{ID: "settle-half", OrderID: orderID, Status: entity.SettlementStatusHeld,
			Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 4750}}})
		f.orders.EXPECT().GetProviderChargeID(mock.Anything, "pi_half").Return("ch_half", nil).Once()
		// The Order's own refund (no ResaleListingID) of the one ticket still
		// Issued.
		f.orders.EXPECT().CreateRefund(mock.Anything, entity.RefundParams{OrderID: orderID, ProviderChargeID: "ch_half", Amount: 5000}).
			Return("re_half", nil).Once()
		commit := f.commits(order)

		got, err := f.uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		assert.Equal(t, entity.SettlementID("settle-half"), commit.SettlementID)
		assert.Equal(t, "re_half", commit.ProviderRefundID)
	})

	t.Run("every ticket resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Every ticket resold"
		// @spec components/usecase/order/refund "Settlement given up by resales"
		const orderID = entity.OrderID("order-all-resold")
		order := paidOrder(orderID, 10000, "pi_all", now.Add(-30*24*time.Hour))
		f := newRefundFixture(t)
		f.reads(order)
		f.cancelledEvent(orderID)
		f.holdsTickets(order, 0, 2)
		f.settledAs(orderID, &entity.Settlement{ID: "settle-all", OrderID: orderID, Status: entity.SettlementStatusReversed,
			Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 9500, ProviderPayoutID: "tr_never"}}})
		// No CreateRefund or ReversePayout is expected: no refund is issued
		// when every ticket was resold, and nothing is clawed back from a
		// settlement given up by resales.
		commit := f.commits(order)

		got, err := f.uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		assert.Empty(t, commit.SettlementID, "no Settlement is given to CommitRefund")
		assert.Empty(t, commit.ProviderRefundID)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Refund recorded — the reason, the amount refunded and the time
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_RecordsTheRefund(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

	// cancellable makes order Paid with a cancelled Event, a Held Settlement
	// and the given tickets, and its charge refunded as re_rec.
	cancellable := func(f *refundFixture, order *entity.Order, issued, voided int) {
		f.reads(order)
		f.cancelledEvent(order.ID)
		f.holdsTickets(order, issued, voided)
		f.heldSettlement(order.ID)
		f.refundsCharge(order, "ch_rec", "re_rec")
	}

	t.Run("tickets voided", func(t *testing.T) {
		t.Parallel()
		// The Tickets themselves are Voided by CommitRefund: the story test
		// TestCancelledConcertStory_CancelledAndRefunded verifies "Tickets voided".
		order := paidOrder("order-rec-voided", 6000, "pi_rec", now.Add(-time.Hour))
		f := newRefundFixture(t)
		cancellable(f, order, 2, 0)
		commit := f.commits(order)

		got, err := f.uc.Refund(ctx, order.ID, usecase.RefundReasonCancellation, now)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		// CommitRefund voids the Order's Issued Tickets with it.
		assert.Equal(t, order.ID, commit.OrderID)
		assert.Equal(t, int64(6000), commit.Amount)
	})

	t.Run("cancellation refund recorded", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Cancellation refund recorded"
		order := paidOrder("order-rec-half", 6000, "pi_rec", now.Add(-time.Hour))
		f := newRefundFixture(t)
		cancellable(f, order, 1, 1)
		commit := f.commits(order)

		got, err := f.uc.Refund(ctx, order.ID, usecase.RefundReasonCancellation, now)

		require.NoError(t, err)
		assert.Equal(t, entity.RefundReasonCancellation, commit.Reason)
		assert.Equal(t, int64(3000), commit.Amount)
		assert.Equal(t, "re_rec", commit.ProviderRefundID)
		assert.True(t, now.Equal(commit.RefundTime))
		require.NotNil(t, got.Refund)
		assert.Equal(t, int64(3000), got.Refund.Amount)
	})

	t.Run("dispute recorded", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Dispute recorded"
		order := paidOrder("order-rec-dispute", 6000, "pi_rec", now.Add(-time.Hour))
		f := newRefundFixture(t)
		f.reads(order)
		f.heldSettlement(order.ID)
		// No CreateRefund is expected: a dispute issues no refund.
		commit := f.commits(order)

		_, err := f.uc.Refund(ctx, order.ID, usecase.RefundReasonDispute, now)

		require.NoError(t, err)
		assert.Equal(t, entity.RefundReasonDispute, commit.Reason)
		assert.Equal(t, int64(0), commit.Amount)
		assert.Empty(t, commit.ProviderRefundID)
	})

	t.Run("concurrent refund", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Concurrent refund"
		order := paidOrder("order-rec-race", 6000, "pi_rec", now.Add(-time.Hour))
		stored := *order
		stored.Status = entity.OrderStatusRefunded
		stored.Refund = &entity.OrderRefund{Reason: entity.RefundReasonCancellation, Amount: 6000, ProviderRefundID: "re_first", RefundTime: now.Add(-time.Minute)}
		f := newRefundFixture(t)
		cancellable(f, order, 2, 0)
		f.refunds.EXPECT().CommitRefund(mock.Anything, mock.Anything).
			Return(apperr.New(codes.FailedPrecondition, "settlement is already reversed")).Once()
		f.reads(&stored)

		got, err := f.uc.Refund(ctx, order.ID, usecase.RefundReasonCancellation, now)

		require.NoError(t, err)
		assert.Equal(t, &stored, got)
	})

	t.Run("concurrent refund without a settlement returns the refund stored first", func(t *testing.T) {
		t.Parallel()
		order := paidOrder("order-rec-race-nosettle", 6000, "pi_rec", now.Add(-time.Hour))
		stored := *order
		stored.Status = entity.OrderStatusRefunded
		stored.Refund = &entity.OrderRefund{Reason: entity.RefundReasonCancellation, Amount: 6000, ProviderRefundID: "re_first", RefundTime: now.Add(-time.Minute)}
		f := newRefundFixture(t)
		f.reads(order)
		f.cancelledEvent(order.ID)
		f.holdsTickets(order, 0, 2)
		f.settledAs(order.ID, &entity.Settlement{ID: "settle-gone", OrderID: order.ID, Status: entity.SettlementStatusReversed})
		// The concurrent call already made the Order Refunded, so this
		// CommitRefund changes nothing and reports no error; the Order read
		// back carries the refund stored first.
		f.refunds.EXPECT().CommitRefund(mock.Anything, mock.Anything).Return(nil).Once()
		f.reads(&stored)

		got, err := f.uc.Refund(ctx, order.ID, usecase.RefundReasonCancellation, now)

		require.NoError(t, err)
		assert.Equal(t, stored.Refund, got.Refund, "the refund stored by the first call, not this call's")
	})

	t.Run("retry after a failure", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Retry after a failure"
		order := paidOrder("order-rec-retry", 6000, "pi_rec", now.Add(-time.Hour))
		f := newRefundFixture(t)
		f.orders.EXPECT().Get(mock.Anything, order.ID).Return(order, nil).Twice()
		f.orders.EXPECT().GetEventID(mock.Anything, order.ID).Return("event-1", nil).Twice()
		state := entity.PublishStateCancelled
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(&entity.Event{ID: "event-1", PublishState: &state}, nil).Twice()
		f.holdsTickets(order, 2, 0)
		f.heldSettlement(order.ID)
		f.orders.EXPECT().GetProviderChargeID(mock.Anything, "pi_rec").Return("ch_rec", nil).Twice()
		var refundRequests []entity.RefundParams
		// The provider refunds once per Order and purpose and returns the
		// same refund for a repeated request.
		f.orders.EXPECT().CreateRefund(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, params entity.RefundParams) (string, error) {
				refundRequests = append(refundRequests, params)
				return "re_retry", nil
			}).Twice()
		f.refunds.EXPECT().CommitRefund(mock.Anything, mock.Anything).Return(apperr.New(codes.Internal, "database unavailable")).Once()
		commit := f.commits(order)

		_, err := f.uc.Refund(ctx, order.ID, usecase.RefundReasonCancellation, now)
		require.Error(t, err)
		_, err = f.uc.Refund(ctx, order.ID, usecase.RefundReasonCancellation, now)

		require.NoError(t, err)
		require.Len(t, refundRequests, 2)
		assert.Equal(t, refundRequests[0], refundRequests[1], "the retry repeats the same refund request, which the provider refunds once")
		assert.Equal(t, "re_retry", commit.ProviderRefundID)
		assert.Equal(t, int64(6000), commit.Amount)
	})
}
