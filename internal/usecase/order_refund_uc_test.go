package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Shared stubs (self-contained; same rationale as settlement_uc_test.go).
// stubSettlementRepo, stubOrderRepo, and stubPaymentProvider are already
// declared in settlement_uc_test.go and issuance_uc_test.go (same package
// usecase_test). The stubs below cover the additional interfaces needed by the
// refund use case.
// ─────────────────────────────────────────────────────────────────────────────

// stubRefundRepo implements entity.RefundRepository for tests. It drives the
// real production commit path — CommitRefund is the single atomic DB boundary.
type stubRefundRepo struct {
	commitRefundFn func(ctx context.Context, commit entity.RefundCommit) error
}

func (s *stubRefundRepo) CommitRefund(ctx context.Context, commit entity.RefundCommit) error {
	if s.commitRefundFn != nil {
		return s.commitRefundFn(ctx, commit)
	}
	return nil
}

// stubRefundSettlementPayouts overrides the payment provider stub's
// CreateRefund and ReversePayout.
type stubRefundSettlementPayouts struct {
	stubPaymentProvider
	createRefundFn    func(ctx context.Context, params entity.RefundParams) (string, error)
	reverseTransferFn func(ctx context.Context, params entity.ReversePayoutParams) (string, error)
}

func (s *stubRefundSettlementPayouts) CreateRefund(ctx context.Context, params entity.RefundParams) (string, error) {
	if s.createRefundFn != nil {
		return s.createRefundFn(ctx, params)
	}
	return "re_test", nil
}

func (s *stubRefundSettlementPayouts) ReversePayout(ctx context.Context, params entity.ReversePayoutParams) (string, error) {
	if s.reverseTransferFn != nil {
		return s.reverseTransferFn(ctx, params)
	}
	return "trr_test", nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// stubOrderTickets implements the TicketRepository read of the refund use
// case: the user "user-<order id>" of a paidOrder holds issued Issued and
// voided Voided tickets of that order (2 Issued by default).
type stubOrderTickets struct {
	entity.TicketRepository
	issued, voided int
}

func (s stubOrderTickets) ListByUser(_ context.Context, userID entity.UserID) ([]*entity.Ticket, error) {
	orderID := entity.OrderID(strings.TrimPrefix(string(userID), "user-"))
	issued := s.issued
	if issued == 0 && s.voided == 0 {
		issued = 2
	}
	var out []*entity.Ticket
	for range issued {
		out = append(out, &entity.Ticket{OrderID: orderID, UserID: userID, Status: entity.TicketStatusIssued})
	}
	for range s.voided {
		out = append(out, &entity.Ticket{OrderID: orderID, UserID: userID, Status: entity.TicketStatusVoided})
	}
	// Another order of the same user is never counted.
	out = append(out, &entity.Ticket{OrderID: "other-order", UserID: userID, Status: entity.TicketStatusIssued})
	return out, nil
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

// refundEvents implements the EventRepository read of the refund use case:
// every Event is in state, and reads are counted.
type refundEvents struct {
	entity.EventRepository
	state entity.PublishState
	reads *int
}

func (s refundEvents) Get(_ context.Context, id string) (*entity.Event, error) {
	if s.reads != nil {
		*s.reads++
	}
	state := s.state
	return &entity.Event{ID: id, PublishState: &state}, nil
}

func newRefundUCWithLogger(
	orderRepo entity.OrderRepository,
	refundRepo entity.RefundRepository,
	settlementRepo entity.SettlementRepository,
	port paymentProviderStub,
	t *testing.T,
	tickets ...stubOrderTickets,
) usecase.OrderUseCase {
	return newRefundUCForEvent(orderRepo, refundRepo, settlementRepo, port, t,
		refundEvents{state: entity.PublishStateCancelled}, tickets...)
}

// newRefundUCForEvent is newRefundUCWithLogger with the Events the Orders are
// for read from events.
func newRefundUCForEvent(
	orderRepo entity.OrderRepository,
	refundRepo entity.RefundRepository,
	settlementRepo entity.SettlementRepository,
	port paymentProviderStub,
	t *testing.T,
	events refundEvents,
	tickets ...stubOrderTickets,
) usecase.OrderUseCase {
	ticketRepo := stubOrderTickets{}
	if len(tickets) > 0 {
		ticketRepo = tickets[0]
	}
	return usecase.NewOrderUseCase(usecase.OrderDeps{
		EventRepo:      events,
		TicketRepo:     ticketRepo,
		OrderRepo:      withOrderPayments(orderRepo, port),
		RefundRepo:     refundRepo,
		SettlementRepo: withSettlementPayouts(settlementRepo, port),
		Logger:         newTestLogger(t),
	})
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

	// Settlement is Released (Transfer already paid out) — reversal must occur.
	settlement := &entity.Settlement{
		ID:               settleID,
		OrderID:          orderID,
		Status:           entity.SettlementStatusReleased,
		ProviderChargeID: providerChargeID,
		Splits: []entity.SettlementSplit{
			{PayeeOrganizerID: "org-1", Amount: 9000, ProviderPayoutID: providerPayoutID},
		},
	}

	var (
		refundCalled   bool
		reversalCalled bool
		commitCalled   bool
	)

	// CommitRefund is the single atomic DB boundary — all three state
	// transitions (settlement→Reversed, tickets→Voided, order→Refunded) happen
	// here in production. The test validates that commit is called with the
	// correct SettlementID and that the reversed split's trr_ ref is passed.
	refundRepo := &stubRefundRepo{
		commitRefundFn: func(_ context.Context, commit entity.RefundCommit) error {
			assert.Equal(t, orderID, commit.OrderID)
			assert.Equal(t, settleID, commit.SettlementID)
			require.Len(t, commit.ReversedSplits, 1)
			assert.Equal(t, "trr_cancel_reversal", commit.ReversedSplits[0].ProviderPayoutReversalID)
			commitCalled = true
			return nil
		},
	}

	port := &stubRefundSettlementPayouts{
		resolveProviderChargeIDFn: func(_ context.Context, _ string) (string, error) {
			return providerChargeID, nil
		},
		createRefundFn: func(_ context.Context, params entity.RefundParams) (string, error) {
			// Fix #2: idempotency key must be keyed on OrderID, not SettlementID.
			assert.Equal(t, orderID, params.OrderID)
			assert.Equal(t, providerChargeID, params.ProviderChargeID)
			assert.Equal(t, int64(10000), params.Amount)
			refundCalled = true
			return "re_cancel", nil
		},
		reverseTransferFn: func(_ context.Context, params entity.ReversePayoutParams) (string, error) {
			assert.Equal(t, providerPayoutID, params.ProviderPayoutID)
			assert.Equal(t, int64(9000), params.Amount)
			reversalCalled = true
			return "trr_cancel_reversal", nil
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, id entity.OrderID) (*entity.Order, error) {
			assert.Equal(t, orderID, id)
			return order, nil
		},
	}

	settleRepo := &stubSettlementRepo{

		getByOrderIDFn: func(_ context.Context, _ entity.OrderID) (*entity.Settlement, error) {
			return settlement, nil
		},
	}

	uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t)

	result, err := uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)

	assert.True(t, refundCalled, "CreateRefund must be called")
	assert.True(t, reversalCalled, "ReverseTransfer must be called for Released settlement")
	assert.True(t, commitCalled, "CommitRefund must be called (atomic DB commit)")
}

// ─────────────────────────────────────────────────────────────────────────────
// CANCELLATION — no settlement yet (payout sweep hasn't run)
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_Cancellation_NoSettlement(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now()
	paidAt := now.Add(-7 * 24 * time.Hour)

	const orderID = entity.OrderID("order-no-settle")

	order := paidOrder(orderID, 5000, "pi_no_settle", paidAt)

	var (
		refundCalled bool
		commitCalled bool
	)

	settleRepo := &stubSettlementRepo{

		getByOrderIDFn: func(_ context.Context, _ entity.OrderID) (*entity.Settlement, error) {
			return nil, apperr.New(apperr.ErrNotFound.Code, "no settlement")
		},
	}

	refundRepo := &stubRefundRepo{
		commitRefundFn: func(_ context.Context, commit entity.RefundCommit) error {
			assert.Equal(t, orderID, commit.OrderID)
			// No settlement → SettlementID must be empty.
			assert.Equal(t, entity.SettlementID(""), commit.SettlementID)
			commitCalled = true
			return nil
		},
	}

	port := &stubRefundSettlementPayouts{
		resolveProviderChargeIDFn: func(_ context.Context, providerPaymentID string) (string, error) {
			assert.Equal(t, "pi_no_settle", providerPaymentID)
			return "ch_no_settle", nil
		},
		createRefundFn: func(_ context.Context, params entity.RefundParams) (string, error) {
			// Fix #2: key is order-based even without a settlement row.
			assert.Equal(t, orderID, params.OrderID)
			refundCalled = true
			return "re_no_settle", nil
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return order, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t)

	result, err := uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	assert.True(t, refundCalled, "CreateRefund must be called even without a settlement row")
	assert.True(t, commitCalled, "CommitRefund must be called")
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

	refundCalled := false
	port := &stubRefundSettlementPayouts{
		createRefundFn: func(_ context.Context, _ entity.RefundParams) (string, error) {
			refundCalled = true
			return "should-not-be-called", nil
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) {
			return alreadyRefunded, nil
		},
	}

	uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, &stubSettlementRepo{}, port, t)

	result, err := uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	assert.False(t, refundCalled, "CreateRefund must NOT be called on idempotent replay")
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

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return pendingOrder, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, &stubSettlementRepo{}, &stubRefundSettlementPayouts{}, t)

	_, err := uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
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

	settlement := &entity.Settlement{
		ID:               settleID,
		OrderID:          orderID,
		Status:           entity.SettlementStatusReleased,
		ProviderChargeID: providerChargeID,
		Splits: []entity.SettlementSplit{
			{PayeeOrganizerID: "org-dispute", Amount: 11000, ProviderPayoutID: providerPayoutID},
		},
	}

	var (
		reversalCalled bool
		commitCalled   bool
	)

	refundRepo := &stubRefundRepo{
		commitRefundFn: func(_ context.Context, commit entity.RefundCommit) error {
			assert.Equal(t, orderID, commit.OrderID)
			assert.Equal(t, settleID, commit.SettlementID)
			require.Len(t, commit.ReversedSplits, 1)
			assert.Equal(t, "trr_dispute", commit.ReversedSplits[0].ProviderPayoutReversalID)
			commitCalled = true
			return nil
		},
	}

	settleRepo := &stubSettlementRepo{

		getByOrderIDFn: func(_ context.Context, _ entity.OrderID) (*entity.Settlement, error) {
			return settlement, nil
		},
	}

	port := &stubRefundSettlementPayouts{
		createRefundFn: func(_ context.Context, params entity.RefundParams) (string, error) {
			assert.Equal(t, providerChargeID, params.ProviderChargeID)
			// Fix #2: key is OrderID-based.
			assert.Equal(t, orderID, params.OrderID)
			return "re_dispute", nil
		},
		reverseTransferFn: func(_ context.Context, params entity.ReversePayoutParams) (string, error) {
			assert.Equal(t, providerPayoutID, params.ProviderPayoutID)
			reversalCalled = true
			return "trr_dispute", nil
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return order, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t)

	result, err := uc.Refund(ctx, orderID, usecase.RefundReasonDispute, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	assert.True(t, reversalCalled, "ReverseTransfer must be called for chargeback-after-payout")
	assert.True(t, commitCalled, "CommitRefund must commit atomically")
}

// ─────────────────────────────────────────────────────────────────────────────
// Event state — a cancellation refund needs a CANCELLED Event; a dispute does not
// ─────────────────────────────────────────────────────────────────────────────

func TestOrderUseCase_Refund_EventState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	const orderID = entity.OrderID("order-on-sale")

	setup := func(t *testing.T) (orderRepo *stubOrderRepo, refundRepo *stubRefundRepo, port *stubRefundSettlementPayouts, moved *bool) {
		t.Helper()
		var m bool
		order := paidOrder(orderID, 10000, "pi_on_sale", now.Add(-24*time.Hour))
		orderRepo = &stubOrderRepo{
			getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return order, nil },
		}
		refundRepo = &stubRefundRepo{
			commitRefundFn: func(_ context.Context, _ entity.RefundCommit) error { m = true; return nil },
		}
		port = &stubRefundSettlementPayouts{
			createRefundFn: func(_ context.Context, _ entity.RefundParams) (string, error) { m = true; return "re_on_sale", nil },
		}
		return orderRepo, refundRepo, port, &m
	}
	noSettlement := &stubSettlementRepo{
		getByOrderIDFn: func(_ context.Context, _ entity.OrderID) (*entity.Settlement, error) {
			return nil, apperr.New(apperr.ErrNotFound.Code, "no settlement")
		},
	}

	t.Run("a cancellation refund for an event still on sale fails and moves no money", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Cancellation refund for an event still on sale"
		// @spec stories/remove-a-cancelled-concert "Refund before cancellation"
		orderRepo, refundRepo, port, moved := setup(t)
		uc := newRefundUCForEvent(orderRepo, refundRepo, noSettlement, port, t,
			refundEvents{state: entity.PublishStatePublished})

		_, err := uc.Refund(context.Background(), orderID, usecase.RefundReasonCancellation, now)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.False(t, *moved, "no refund and no commit")
	})

	t.Run("a dispute for an event still on sale is refunded without reading the event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Dispute for an event still on sale"
		orderRepo, refundRepo, port, _ := setup(t)
		var reads int
		uc := newRefundUCForEvent(orderRepo, refundRepo, noSettlement, port, t,
			refundEvents{state: entity.PublishStatePublished, reads: &reads})

		got, err := uc.Refund(context.Background(), orderID, usecase.RefundReasonDispute, now)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		assert.Zero(t, reads, "the Event's state is not checked")
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

	uc := newRefundUCWithLogger(&stubOrderRepo{}, &stubRefundRepo{}, &stubSettlementRepo{}, &stubRefundSettlementPayouts{}, t)

	_, err := uc.Refund(ctx, "order-unspec", usecase.RefundReasonUnspecified, now)
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

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) {
			return nil, apperr.New(apperr.ErrNotFound.Code, "order not found")
		},
	}

	uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, &stubSettlementRepo{}, &stubRefundSettlementPayouts{}, t)

	_, err := uc.Refund(ctx, "order-missing", usecase.RefundReasonCancellation, time.Now())
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

	settlement := &entity.Settlement{
		ID:               settleID,
		OrderID:          orderID,
		Status:           entity.SettlementStatusHeld, // not yet released
		ProviderChargeID: "ch_held",
		Splits: []entity.SettlementSplit{
			// No ProviderPayoutID — not yet paid out.
			{PayeeOrganizerID: "org-held", Amount: 7000},
		},
	}

	var (
		reversalCalled bool
		commitCalled   bool
	)

	refundRepo := &stubRefundRepo{
		commitRefundFn: func(_ context.Context, commit entity.RefundCommit) error {
			// Fix #6: even for Held settlements, CommitRefund must be called with
			// the SettlementID so it flips the settlement to Reversed and prevents
			// the payout sweeper from releasing the refunded order's funds.
			assert.Equal(t, settleID, commit.SettlementID)
			assert.Equal(t, orderID, commit.OrderID)
			commitCalled = true
			return nil
		},
	}

	settleRepo := &stubSettlementRepo{

		getByOrderIDFn: func(_ context.Context, _ entity.OrderID) (*entity.Settlement, error) {
			return settlement, nil
		},
	}

	port := &stubRefundSettlementPayouts{
		createRefundFn: func(_ context.Context, _ entity.RefundParams) (string, error) {
			return "re_held", nil
		},
		reverseTransferFn: func(_ context.Context, _ entity.ReversePayoutParams) (string, error) {
			reversalCalled = true
			return "should-not-be-called", nil
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return order, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t)

	result, err := uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	// No transfers to reverse (Held = not yet paid out).
	assert.False(t, reversalCalled, "ReverseTransfer must NOT be called for a Held settlement")
	// But CommitRefund MUST still be called to flip the settlement to Reversed
	// (Fix #6: prevents payout sweeper from releasing the refunded order).
	assert.True(t, commitCalled, "CommitRefund must be called even for Held settlement (TOCTOU fix)")
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

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return komojuOrder, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, &stubSettlementRepo{}, &stubRefundSettlementPayouts{}, t)

	_, err := uc.Refund(ctx, "order-komoju", usecase.RefundReasonCancellation, time.Now())
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

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return badOrder, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, &stubSettlementRepo{}, &stubRefundSettlementPayouts{}, t)

	_, err := uc.Refund(ctx, "order-no-pi", usecase.RefundReasonCancellation, time.Now())
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

	settleRepo := &stubSettlementRepo{

		getByOrderIDFn: func(_ context.Context, _ entity.OrderID) (*entity.Settlement, error) {
			return nil, apperr.New(apperr.ErrNotFound.Code, "no settlement")
		},
	}

	// CommitRefund returns FailedPrecondition to simulate a concurrent refund
	// that already set the settlement to Reversed between our Stripe calls and
	// our DB commit.
	refundRepo := &stubRefundRepo{
		commitRefundFn: func(_ context.Context, _ entity.RefundCommit) error {
			return apperr.ErrFailedPrecondition
		},
	}

	// The use case must re-read the order on FailedPrecondition to return the
	// post-refund state. After the concurrent refund the order is Refunded.
	refundedOrder := &entity.Order{ID: orderID, Status: entity.OrderStatusRefunded, Amount: 9000,
		Payment: entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_concurrent"}}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, id entity.OrderID) (*entity.Order, error) {
			// First call: return Paid (pre-refund check). Second call: return Refunded (re-read).
			if id == orderID {
				return order, nil
			}
			return refundedOrder, nil
		},
	}
	// Override: first Get returns Paid; after CommitRefund fails, Get re-reads.
	callCount := 0
	orderRepo.getFn = func(_ context.Context, _ entity.OrderID) (*entity.Order, error) {
		callCount++
		if callCount == 1 {
			return order, nil
		}
		return refundedOrder, nil
	}

	port := &stubRefundSettlementPayouts{
		resolveProviderChargeIDFn: func(_ context.Context, _ string) (string, error) { return "ch_concurrent", nil },
		createRefundFn:            func(_ context.Context, _ entity.RefundParams) (string, error) { return "re_concurrent", nil },
	}

	uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t)

	result, err := uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	// Returns the current (post-concurrent-refund) state.
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
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
		settleRepo := &stubSettlementRepo{
			getByOrderIDFn: func(context.Context, entity.OrderID) (*entity.Settlement, error) {
				return &entity.Settlement{ID: "settle-half", OrderID: orderID, Status: entity.SettlementStatusHeld,
					Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 4750}}}, nil
			},
		}
		var refunded int64
		port := &stubRefundSettlementPayouts{
			resolveProviderChargeIDFn: func(context.Context, string) (string, error) { return "ch_half", nil },
			createRefundFn: func(_ context.Context, params entity.RefundParams) (string, error) {
				assert.Empty(t, params.ResaleListingID, "the Order's own refund")
				refunded = params.Amount
				return "re_half", nil
			},
		}
		var commit entity.RefundCommit
		refundRepo := &stubRefundRepo{commitRefundFn: func(_ context.Context, c entity.RefundCommit) error { commit = c; return nil }}
		orderRepo := &stubOrderRepo{getFn: func(context.Context, entity.OrderID) (*entity.Order, error) { return order, nil }}
		uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t, stubOrderTickets{issued: 1, voided: 1})

		got, err := uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		assert.Equal(t, int64(5000), refunded)
		assert.Equal(t, entity.SettlementID("settle-half"), commit.SettlementID)
		assert.Equal(t, "re_half", commit.ProviderRefundID)
	})

	t.Run("every ticket resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/refund "Every ticket resold"
		// @spec components/usecase/order/refund "Settlement given up by resales"
		const orderID = entity.OrderID("order-all-resold")
		order := paidOrder(orderID, 10000, "pi_all", now.Add(-30*24*time.Hour))
		settleRepo := &stubSettlementRepo{
			getByOrderIDFn: func(context.Context, entity.OrderID) (*entity.Settlement, error) {
				return &entity.Settlement{ID: "settle-all", OrderID: orderID, Status: entity.SettlementStatusReversed,
					Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 9500, ProviderPayoutID: "tr_never"}}}, nil
			},
		}
		port := &stubRefundSettlementPayouts{
			createRefundFn: func(context.Context, entity.RefundParams) (string, error) {
				assert.Fail(t, "no refund is issued when every ticket was resold")
				return "", nil
			},
			reverseTransferFn: func(context.Context, entity.ReversePayoutParams) (string, error) {
				assert.Fail(t, "nothing is clawed back from a settlement given up by resales")
				return "", nil
			},
		}
		var commit entity.RefundCommit
		committed := false
		refundRepo := &stubRefundRepo{commitRefundFn: func(_ context.Context, c entity.RefundCommit) error {
			commit, committed = c, true
			return nil
		}}
		orderRepo := &stubOrderRepo{getFn: func(context.Context, entity.OrderID) (*entity.Order, error) { return order, nil }}
		uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t, stubOrderTickets{voided: 2})

		got, err := uc.Refund(ctx, orderID, usecase.RefundReasonCancellation, now)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		require.True(t, committed)
		assert.Empty(t, commit.SettlementID, "no Settlement is given to CommitRefund")
		assert.Empty(t, commit.ProviderRefundID)
	})
}
