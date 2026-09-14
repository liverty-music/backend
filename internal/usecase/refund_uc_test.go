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
// Shared stubs (self-contained; same rationale as settlement_uc_test.go).
// stubSettlementRepo, stubOrderRepo, and stubPaymentSettlementPort are already
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

// stubRefundSettlementPort extends stubPaymentSettlementPort to support the
// new CreateRefund and ReverseTransfer methods.
type stubRefundSettlementPort struct {
	stubPaymentSettlementPort
	createRefundFn    func(ctx context.Context, params usecase.RefundParams) (string, error)
	reverseTransferFn func(ctx context.Context, params usecase.ReverseTransferParams) (string, error)
}

func (s *stubRefundSettlementPort) CreateRefund(ctx context.Context, params usecase.RefundParams) (string, error) {
	if s.createRefundFn != nil {
		return s.createRefundFn(ctx, params)
	}
	return "re_test", nil
}

func (s *stubRefundSettlementPort) ReverseTransfer(ctx context.Context, params usecase.ReverseTransferParams) (string, error) {
	if s.reverseTransferFn != nil {
		return s.reverseTransferFn(ctx, params)
	}
	return "trr_test", nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func paidOrder(id entity.OrderID, amount int64, piRef string, paidAt time.Time) *entity.Order {
	return &entity.Order{
		ID:       id,
		Status:   entity.OrderStatusPaid,
		Amount:   amount,
		Currency: "JPY",
		PaidTime: paidAt,
		Payment: entity.Payment{
			Provider:         entity.PaymentProviderStripe,
			PaymentIntentRef: piRef,
		},
	}
}

func newRefundUCWithLogger(
	orderRepo entity.OrderRepository,
	refundRepo entity.RefundRepository,
	settlementRepo entity.SettlementRepository,
	port usecase.PaymentSettlementPort,
	t *testing.T,
) usecase.RefundOrderUseCase {
	return usecase.NewRefundOrderUseCase(orderRepo, refundRepo, settlementRepo, port, newTestLogger(t))
}

// ─────────────────────────────────────────────────────────────────────────────
// CANCELLATION — always allowed while Order is Paid; Released settlement
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_Cancellation_HappyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	paidAt := now.Add(-30 * 24 * time.Hour)

	const (
		orderID     = entity.OrderID("order-cancel")
		settleID    = entity.SettlementID("settle-cancel")
		transferRef = "tr_cancel"
		chRef       = "ch_cancel"
		piRef       = "pi_cancel"
	)

	order := paidOrder(orderID, 10000, piRef, paidAt)

	// Settlement is Released (Transfer already paid out) — reversal must occur.
	settlement := &entity.Settlement{
		ID:        settleID,
		OrderID:   orderID,
		Status:    entity.SettlementStatusReleased,
		ChargeRef: chRef,
		Splits: []entity.SettlementSplit{
			{PayeeOrganizerID: "org-1", Amount: 9000, TransferRef: transferRef},
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
			assert.Equal(t, "trr_cancel_reversal", commit.ReversedSplits[0].TransferReversalRef)
			commitCalled = true
			return nil
		},
	}

	port := &stubRefundSettlementPort{
		stubPaymentSettlementPort: stubPaymentSettlementPort{
			resolveChargeRefFn: func(_ context.Context, _ string) (string, error) {
				return chRef, nil
			},
		},
		createRefundFn: func(_ context.Context, params usecase.RefundParams) (string, error) {
			// Fix #2: idempotency key must be keyed on OrderID, not SettlementID.
			assert.Equal(t, orderID, params.OrderID)
			assert.Equal(t, chRef, params.ChargeRef)
			assert.Equal(t, int64(10000), params.Amount)
			refundCalled = true
			return "re_cancel", nil
		},
		reverseTransferFn: func(_ context.Context, params usecase.ReverseTransferParams) (string, error) {
			assert.Equal(t, transferRef, params.TransferRef)
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

	result, err := uc.RefundOrder(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)

	assert.True(t, refundCalled, "CreateRefund must be called")
	assert.True(t, reversalCalled, "ReverseTransfer must be called for Released settlement")
	assert.True(t, commitCalled, "CommitRefund must be called (atomic DB commit)")
}

// ─────────────────────────────────────────────────────────────────────────────
// CANCELLATION — no settlement yet (payout sweep hasn't run)
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_Cancellation_NoSettlement(t *testing.T) {
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

	port := &stubRefundSettlementPort{
		stubPaymentSettlementPort: stubPaymentSettlementPort{
			resolveChargeRefFn: func(_ context.Context, piRef string) (string, error) {
				assert.Equal(t, "pi_no_settle", piRef)
				return "ch_no_settle", nil
			},
		},
		createRefundFn: func(_ context.Context, params usecase.RefundParams) (string, error) {
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

	result, err := uc.RefundOrder(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	assert.True(t, refundCalled, "CreateRefund must be called even without a settlement row")
	assert.True(t, commitCalled, "CommitRefund must be called")
}

// ─────────────────────────────────────────────────────────────────────────────
// Idempotent replay — already-refunded Order is returned unchanged
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_IdempotentReplay_AlreadyRefunded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now()

	const orderID = entity.OrderID("order-already-refunded")

	alreadyRefunded := &entity.Order{
		ID:     orderID,
		Status: entity.OrderStatusRefunded,
		Amount: 8000,
		Payment: entity.Payment{
			Provider:         entity.PaymentProviderStripe,
			PaymentIntentRef: "pi_already",
		},
	}

	refundCalled := false
	port := &stubRefundSettlementPort{
		createRefundFn: func(_ context.Context, _ usecase.RefundParams) (string, error) {
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

	result, err := uc.RefundOrder(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	assert.False(t, refundCalled, "CreateRefund must NOT be called on idempotent replay")
}

// ─────────────────────────────────────────────────────────────────────────────
// Not-refundable — Failed order returns FAILED_PRECONDITION
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_NotRefundable_FailedOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now()

	const orderID = entity.OrderID("order-failed")

	failedOrder := &entity.Order{
		ID:     orderID,
		Status: entity.OrderStatusFailed,
		Amount: 6000,
		Payment: entity.Payment{
			Provider:         entity.PaymentProviderStripe,
			PaymentIntentRef: "pi_failed",
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return failedOrder, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, &stubSettlementRepo{},
		&stubRefundSettlementPort{}, t)

	_, err := uc.RefundOrder(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "expected FailedPrecondition for a non-Paid order")
}

// ─────────────────────────────────────────────────────────────────────────────
// POSTPONEMENT_WINDOW — admin call is authoritative; no time-gate in code
// Fix #7: the PaidTime-based deadline is removed. The admin call is the policy
// gate; code enforces no additional rejection. Test validates POSTPONEMENT_WINDOW
// succeeds regardless of elapsed time (even far in the future).
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_PostponementWindow_AdminCallIsAuthoritative(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	paidAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		now  time.Time
	}{
		{
			name: "immediately after purchase",
			now:  paidAt.Add(1 * time.Hour),
		},
		{
			name: "10 days after purchase",
			now:  paidAt.Add(10 * 24 * time.Hour),
		},
		{
			name: "far in the future — still allowed (admin is the gate)",
			// Previously this would have been rejected by the PaidTime+14d window.
			// Fix #7: PaidTime window is removed; admin call is authoritative.
			now: paidAt.Add(365 * 24 * time.Hour),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const orderID = entity.OrderID("order-postpone")

			order := paidOrder(orderID, 7000, "pi_postpone", paidAt)

			settleRepo := &stubSettlementRepo{

				getByOrderIDFn: func(_ context.Context, _ entity.OrderID) (*entity.Settlement, error) {
					return nil, apperr.New(apperr.ErrNotFound.Code, "no settlement")
				},
			}

			refundCalled := false
			port := &stubRefundSettlementPort{
				stubPaymentSettlementPort: stubPaymentSettlementPort{
					resolveChargeRefFn: func(_ context.Context, _ string) (string, error) {
						return "ch_postpone", nil
					},
				},
				createRefundFn: func(_ context.Context, _ usecase.RefundParams) (string, error) {
					refundCalled = true
					return "re_postpone", nil
				},
			}

			orderRepo := &stubOrderRepo{
				getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return order, nil },
			}

			uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, settleRepo, port, t)

			result, err := uc.RefundOrder(ctx, orderID, usecase.RefundReasonPostponementWindow, tc.now)
			require.NoError(t, err, "POSTPONEMENT_WINDOW must succeed; admin call is the policy gate")
			assert.Equal(t, entity.OrderStatusRefunded, result.Status)
			assert.True(t, refundCalled)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DISPUTE — chargeback-after-payout reverses the transfer
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_Dispute_AfterPayout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now()
	paidAt := now.Add(-60 * 24 * time.Hour)

	const (
		orderID     = entity.OrderID("order-dispute")
		settleID    = entity.SettlementID("settle-dispute")
		transferRef = "tr_dispute"
		chRef       = "ch_dispute"
	)

	order := paidOrder(orderID, 12000, "pi_dispute", paidAt)

	settlement := &entity.Settlement{
		ID:        settleID,
		OrderID:   orderID,
		Status:    entity.SettlementStatusReleased,
		ChargeRef: chRef,
		Splits: []entity.SettlementSplit{
			{PayeeOrganizerID: "org-dispute", Amount: 11000, TransferRef: transferRef},
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
			assert.Equal(t, "trr_dispute", commit.ReversedSplits[0].TransferReversalRef)
			commitCalled = true
			return nil
		},
	}

	settleRepo := &stubSettlementRepo{

		getByOrderIDFn: func(_ context.Context, _ entity.OrderID) (*entity.Settlement, error) {
			return settlement, nil
		},
	}

	port := &stubRefundSettlementPort{
		createRefundFn: func(_ context.Context, params usecase.RefundParams) (string, error) {
			assert.Equal(t, chRef, params.ChargeRef)
			// Fix #2: key is OrderID-based.
			assert.Equal(t, orderID, params.OrderID)
			return "re_dispute", nil
		},
		reverseTransferFn: func(_ context.Context, params usecase.ReverseTransferParams) (string, error) {
			assert.Equal(t, transferRef, params.TransferRef)
			reversalCalled = true
			return "trr_dispute", nil
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return order, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t)

	result, err := uc.RefundOrder(ctx, orderID, usecase.RefundReasonDispute, now)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
	assert.True(t, reversalCalled, "ReverseTransfer must be called for chargeback-after-payout")
	assert.True(t, commitCalled, "CommitRefund must commit atomically")
}

// ─────────────────────────────────────────────────────────────────────────────
// UNSPECIFIED reason — returns InvalidArgument
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_UnspecifiedReason_InvalidArgument(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now()

	uc := newRefundUCWithLogger(&stubOrderRepo{}, &stubRefundRepo{}, &stubSettlementRepo{},
		&stubRefundSettlementPort{}, t)

	_, err := uc.RefundOrder(ctx, "order-unspec", usecase.RefundReasonUnspecified, now)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument, "expected InvalidArgument for unspecified reason")
}

// ─────────────────────────────────────────────────────────────────────────────
// Order not found — returns NotFound
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_OrderNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) {
			return nil, apperr.New(apperr.ErrNotFound.Code, "order not found")
		},
	}

	uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, &stubSettlementRepo{},
		&stubRefundSettlementPort{}, t)

	_, err := uc.RefundOrder(ctx, "order-missing", usecase.RefundReasonCancellation, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrNotFound, "expected NotFound")
}

// ─────────────────────────────────────────────────────────────────────────────
// Held settlement — no transfer to reverse; CommitRefund still called to flip
// settlement to Reversed (Fix #6 TOCTOU: blocks payout sweeper from releasing)
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_HeldSettlement_NoReversal_ButCommitCalled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now()
	paidAt := now.Add(-5 * 24 * time.Hour)

	const (
		orderID  = entity.OrderID("order-held")
		settleID = entity.SettlementID("settle-held")
	)

	order := paidOrder(orderID, 8000, "pi_held", paidAt)

	settlement := &entity.Settlement{
		ID:        settleID,
		OrderID:   orderID,
		Status:    entity.SettlementStatusHeld, // not yet released
		ChargeRef: "ch_held",
		Splits: []entity.SettlementSplit{
			// No TransferRef — not yet paid out.
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

	port := &stubRefundSettlementPort{
		createRefundFn: func(_ context.Context, _ usecase.RefundParams) (string, error) {
			return "re_held", nil
		},
		reverseTransferFn: func(_ context.Context, _ usecase.ReverseTransferParams) (string, error) {
			reversalCalled = true
			return "should-not-be-called", nil
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return order, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t)

	result, err := uc.RefundOrder(ctx, orderID, usecase.RefundReasonCancellation, now)
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

func TestRefundOrder_NonStripeProvider_FailedPrecondition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	komojuOrder := &entity.Order{
		ID:     "order-komoju",
		Status: entity.OrderStatusPaid,
		Amount: 5000,
		Payment: entity.Payment{
			Provider:         entity.PaymentProviderKOMOJU,
			PaymentIntentRef: "pi_komoju",
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return komojuOrder, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, &stubSettlementRepo{},
		&stubRefundSettlementPort{}, t)

	_, err := uc.RefundOrder(ctx, "order-komoju", usecase.RefundReasonCancellation, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"non-Stripe provider must return FailedPrecondition (no Stripe refund path)")
}

// ─────────────────────────────────────────────────────────────────────────────
// Fix #8: empty PaymentIntentRef returns FailedPrecondition
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_EmptyPaymentIntentRef_FailedPrecondition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	badOrder := &entity.Order{
		ID:     "order-no-pi",
		Status: entity.OrderStatusPaid,
		Amount: 5000,
		Payment: entity.Payment{
			Provider:         entity.PaymentProviderStripe,
			PaymentIntentRef: "", // empty — missing ref
		},
	}

	orderRepo := &stubOrderRepo{
		getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) { return badOrder, nil },
	}

	uc := newRefundUCWithLogger(orderRepo, &stubRefundRepo{}, &stubSettlementRepo{},
		&stubRefundSettlementPort{}, t)

	_, err := uc.RefundOrder(ctx, "order-no-pi", usecase.RefundReasonCancellation, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"empty PaymentIntentRef must return FailedPrecondition")
}

// ─────────────────────────────────────────────────────────────────────────────
// Fix #3: CommitRefund FailedPrecondition (concurrent refund) is handled
// ─────────────────────────────────────────────────────────────────────────────

func TestRefundOrder_ConcurrentRefund_IdempotentViaCommitFailedPrecondition(t *testing.T) {
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
		Payment: entity.Payment{Provider: entity.PaymentProviderStripe, PaymentIntentRef: "pi_concurrent"}}

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

	port := &stubRefundSettlementPort{
		stubPaymentSettlementPort: stubPaymentSettlementPort{
			resolveChargeRefFn: func(_ context.Context, _ string) (string, error) { return "ch_concurrent", nil },
		},
		createRefundFn: func(_ context.Context, _ usecase.RefundParams) (string, error) { return "re_concurrent", nil },
	}

	uc := newRefundUCWithLogger(orderRepo, refundRepo, settleRepo, port, t)

	result, err := uc.RefundOrder(ctx, orderID, usecase.RefundReasonCancellation, now)
	require.NoError(t, err)
	// Returns the current (post-concurrent-refund) state.
	assert.Equal(t, entity.OrderStatusRefunded, result.Status)
}
