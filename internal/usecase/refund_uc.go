package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// RefundOrderUseCase handles ⑤'s refund policy and delegates the actual money
// movement to the settlement layer. ⑤ owns the policy (why + who); settlement
// owns the execution (Refund + transfer_reversal + status transitions).
type RefundOrderUseCase interface {
	// RefundOrder applies the refund policy for the given reason and executes
	// the money-movement: issues a platform-balance Refund, voids the Order's
	// tickets, sets Order status=refunded, and (if a Settlement exists) reverses
	// any paid-out Transfer(s) and marks the Settlement reversed.
	//
	// It is IDEMPOTENT: a replayed call on an already-refunded Order is a
	// no-op that returns the existing Order.
	//
	// # Policy by reason
	//
	//   - CANCELLATION: always allowed while the Order is Paid. Refunds current
	//     holder (face + system/発券 fee; processor fee retained).
	//   - POSTPONEMENT_WINDOW: the admin call is the policy gate; code enforces
	//     no additional time-based rejection. See the TODO below.
	//   - DISPUTE: always allowed (platform absorbs via negative-balance
	//     responsibility); funds already paid out are clawed back via
	//     transfer_reversal.
	//
	// # Atomicity
	//
	// Stripe money movements (CreateRefund, ReverseTransfer) are performed
	// before the DB commit. Both are idempotent, so a crash between Stripe and
	// the DB commit is recovered safely: on retry the Stripe calls replay via
	// idempotency keys and the DB commit is reattempted.
	//
	// # Possible errors
	//
	//  - NotFound: the Order does not exist.
	//  - FailedPrecondition: the Order is not refundable (already refunded).
	//  - InvalidArgument: the reason is UNSPECIFIED.
	//  - Internal: database or payment provider failure.
	RefundOrder(ctx context.Context, orderID entity.OrderID, reason RefundReason, now time.Time) (*entity.Order, error)
}

// RefundReason mirrors the proto enum liverty_music.rpc.admin.v1.RefundReason;
// defined here as an entity-layer type so the use case layer is independent of
// generated proto code.
type RefundReason int32

const (
	// RefundReasonUnspecified is the zero value; rejected at the boundary.
	RefundReasonUnspecified RefundReason = 0
	// RefundReasonCancellation is an event cancellation (中止): full holder
	// refund (face + system/発券 fee; processor fee retained). Always allowed
	// while Order is Paid.
	RefundReasonCancellation RefundReason = 1
	// RefundReasonPostponementWindow is a holder-initiated refund for a
	// postponed event. The admin call is treated as authoritative — the admin
	// enforces the refund window operationally. No time-based gate is applied
	// in code.
	//
	// TODO: enforce the holder-refund window against an event
	// reschedule/announcement timestamp once that timestamp is modeled in the
	// data layer and surfaced on RefundOrderRequest. A gate based on
	// Order.PaidTime is incorrect because it measures time since purchase, not
	// since the postponement announcement, causing legitimate holder refunds
	// for advance-purchased tickets to be wrongly rejected. This needs a spec
	// and proto change before it can be implemented.
	RefundReasonPostponementWindow RefundReason = 2
	// RefundReasonDispute is a chargeback / dispute. Funds clawed back via
	// transfer_reversal; platform absorbs negative-balance responsibility.
	RefundReasonDispute RefundReason = 3
)

// String returns the human-readable reason name.
func (r RefundReason) String() string {
	switch r {
	case RefundReasonCancellation:
		return "cancellation"
	case RefundReasonPostponementWindow:
		return "postponement_window"
	case RefundReasonDispute:
		return "dispute"
	default:
		return "UNSPECIFIED"
	}
}

// refundOrderUseCase implements [RefundOrderUseCase].
type refundOrderUseCase struct {
	orderRepo      entity.OrderRepository
	refundRepo     entity.RefundRepository
	settlementRepo entity.SettlementRepository
	settlementPort PaymentSettlementPort
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ RefundOrderUseCase = (*refundOrderUseCase)(nil)

// NewRefundOrderUseCase constructs a RefundOrderUseCase with the given
// dependencies. All parameters are required and must not be nil.
func NewRefundOrderUseCase(
	orderRepo entity.OrderRepository,
	refundRepo entity.RefundRepository,
	settlementRepo entity.SettlementRepository,
	settlementPort PaymentSettlementPort,
	logger *logging.Logger,
) RefundOrderUseCase {
	return &refundOrderUseCase{
		orderRepo:      orderRepo,
		refundRepo:     refundRepo,
		settlementRepo: settlementRepo,
		settlementPort: settlementPort,
		logger:         logger,
	}
}

// RefundOrder implements [RefundOrderUseCase].
func (uc *refundOrderUseCase) RefundOrder(ctx context.Context, orderID entity.OrderID, reason RefundReason, _ time.Time) (*entity.Order, error) {
	if reason == RefundReasonUnspecified {
		return nil, apperr.New(codes.InvalidArgument, "refund reason must be specified")
	}

	order, err := uc.orderRepo.Get(ctx, orderID)
	if err != nil {
		return nil, err // propagates NotFound
	}

	// -- idempotency: already refunded → return as-is (no double-refund). --
	if order.Status == entity.OrderStatusRefunded {
		uc.logger.Info(ctx, "refund order: already refunded (idempotent replay)",
			slog.String("order_id", string(orderID)),
			slog.String("reason", reason.String()),
		)
		return order, nil
	}

	// Only Paid orders may be refunded. Failed orders have no hold to refund.
	if order.Status != entity.OrderStatusPaid {
		return nil, apperr.New(codes.FailedPrecondition,
			"order is not in a refundable state (must be paid)")
	}

	// Fix #8: Guard against empty PaymentIntentRef or non-Stripe provider before
	// attempting ResolveChargeRef. A non-Stripe order has no charge in the
	// Stripe sense; an empty ref would send "" to the Stripe API.
	if order.Payment.Provider != entity.PaymentProviderStripe {
		return nil, apperr.New(codes.FailedPrecondition,
			"refund via Stripe is only supported for Stripe-backed orders")
	}
	if order.Payment.PaymentIntentRef == "" {
		return nil, apperr.New(codes.FailedPrecondition,
			"order has no payment intent reference; cannot resolve charge for refund")
	}

	// -- load the settlement (may not exist yet if payout sweep hasn't run). --
	settlement, err := uc.settlementRepo.GetByOrderID(ctx, orderID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	// -- resolve the charge ref needed for the platform-balance Refund.
	// Prefers the charge ref already stored on the settlement row (set by the
	// payout sweep). Falls back to a live PaymentIntent retrieval when the
	// settlement doesn't exist yet or the charge_ref is not yet recorded. --
	chargeRef, err := uc.resolveChargeRef(ctx, order, settlement)
	if err != nil {
		return nil, err
	}

	// ── STRIPE MONEY MOVEMENT ──────────────────────────────────────────────
	// Both calls carry stable idempotency keys so retries are safe:
	//   CreateRefund:    key = "order-refund:<orderID>"
	//   ReverseTransfer: key = "settlement-reversal:<settlementID>:<transferRef>"
	// A crash after Stripe but before the DB commit means the next invocation
	// replays Stripe calls (no-ops at provider) and re-attempts the DB tx.

	// -- issue the platform-balance Refund. --
	//
	// Design decision on the refund amount:
	//   We refund the full Order.Amount (face + system/発券 fee). The processor
	//   fee (Stripe's cut) is not refunded — the JP norm. Stripe deducts the
	//   processor fee from the platform balance on a partial refund. We do not
	//   have the exact Stripe processor fee without expanding the
	//   BalanceTransaction, so MVP accepts this: the platform absorbs the fee.
	//   TODO: if the business requires retaining the processor fee precisely,
	//   expand the charge's BalanceTransaction and subtract net_fee_amount.
	_, err = uc.settlementPort.CreateRefund(ctx, RefundParams{
		OrderID:   orderID,
		ChargeRef: chargeRef,
		Amount:    order.Amount,
	})
	if err != nil {
		return nil, err
	}

	// -- reverse any already-paid-out Transfer(s) per split. --
	// For Held settlements (not yet paid out) there are no transfers to reverse,
	// but we still flip the settlement to Reversed in the DB commit to prevent
	// the payout sweeper from later releasing it (Fix #6 TOCTOU).
	var reversedSplits []entity.SettlementSplit
	if settlement != nil && settlement.Status == entity.SettlementStatusReleased {
		reversedSplits, err = uc.reverseSplits(ctx, settlement)
		if err != nil {
			return nil, err
		}
	} else if settlement != nil {
		// Held settlement: copy splits without reversal refs; they will be
		// persisted as-is by CommitRefund (no trr_ update needed) — the status
		// flip to Reversed is the load-bearing change that blocks the sweeper.
		reversedSplits = settlement.Splits
	}

	// ── ATOMIC DB COMMIT ──────────────────────────────────────────────────
	// Commits all three DB mutations in one transaction:
	//   1. Settlement → Reversed (if settlement exists)
	//   2. Tickets → Voided
	//   3. Order → Refunded
	var settleID entity.SettlementID
	if settlement != nil {
		settleID = settlement.ID
	}

	commit := entity.RefundCommit{
		OrderID:        orderID,
		SettlementID:   settleID,
		ReversedSplits: reversedSplits,
	}
	if err := uc.refundRepo.CommitRefund(ctx, commit); err != nil {
		// FailedPrecondition from CommitRefund means the settlement was already
		// reversed by a concurrent call. The Order/ticket flips may or may not
		// have committed in the concurrent call; re-read and return.
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			uc.logger.Info(ctx, "refund order: concurrent refund applied; reading current order state",
				slog.String("order_id", string(orderID)),
			)
			return uc.orderRepo.Get(ctx, orderID)
		}
		return nil, err
	}

	order.Status = entity.OrderStatusRefunded
	uc.logger.Info(ctx, "refund order: completed",
		slog.String("order_id", string(orderID)),
		slog.String("reason", reason.String()),
		slog.String("charge_ref", chargeRef),
	)
	return order, nil
}

// resolveChargeRef returns the charge ref. Prefers the value already stored on
// the settlement row (populated by the payout sweep), then falls back to a
// live PaymentIntent retrieval.
func (uc *refundOrderUseCase) resolveChargeRef(ctx context.Context, order *entity.Order, settlement *entity.Settlement) (string, error) {
	if settlement != nil && settlement.ChargeRef != "" {
		return settlement.ChargeRef, nil
	}
	return uc.settlementPort.ResolveChargeRef(ctx, order.Payment.PaymentIntentRef)
}

// reverseSplits calls ReverseTransfer for every already-released split and
// returns the updated splits with TransferReversalRef populated. Splits that
// have no TransferRef (never transferred) or already have a TransferReversalRef
// (already reversed) are skipped idempotently.
func (uc *refundOrderUseCase) reverseSplits(ctx context.Context, settlement *entity.Settlement) ([]entity.SettlementSplit, error) {
	// Work on a copy so the original settlement slice is not mutated until we
	// know all reversals succeeded.
	result := make([]entity.SettlementSplit, len(settlement.Splits))
	copy(result, settlement.Splits)

	for i := range result {
		split := &result[i]
		if split.TransferRef == "" {
			// Split was never transferred; skip.
			continue
		}
		if split.TransferReversalRef != "" {
			// Already reversed; idempotent skip.
			continue
		}
		reversalRef, err := uc.settlementPort.ReverseTransfer(ctx, ReverseTransferParams{
			SettlementID: settlement.ID,
			TransferRef:  split.TransferRef,
			Amount:       split.Amount,
		})
		if err != nil {
			return nil, err
		}
		split.TransferReversalRef = reversalRef
	}
	return result, nil
}
