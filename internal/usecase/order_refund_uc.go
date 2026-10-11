package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// RefundReason mirrors the proto enum liverty_music.rpc.admin.order.v1.RefundReason;
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
	// RefundReasonDispute is a chargeback / dispute. Funds clawed back via
	// transfer_reversal; platform absorbs negative-balance responsibility.
	RefundReasonDispute RefundReason = 3
)

// String returns the human-readable reason name.
func (r RefundReason) String() string {
	switch r {
	case RefundReasonCancellation:
		return "cancellation"
	case RefundReasonDispute:
		return "dispute"
	default:
		return "UNSPECIFIED"
	}
}

// Refund implements [OrderUseCase].
func (uc *orderUseCase) Refund(ctx context.Context, orderID entity.OrderID, reason RefundReason, now time.Time) (*entity.Order, error) {
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

	// -- load the settlement (may not exist yet if payout sweep hasn't run). --
	settlement, err := uc.settlementRepo.GetByOrderID(ctx, orderID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	// ── STRIPE MONEY MOVEMENT ──────────────────────────────────────────────
	// Both calls carry stable idempotency keys so retries are safe:
	//   CreateRefund:    key = "order-refund:<orderID>"
	//   ReversePayout: key = "settlement-reversal:<settlementID>:<transferRef>"
	// A crash after Stripe but before the DB commit means the next invocation
	// replays Stripe calls (no-ops at provider) and re-attempts the DB tx.

	// -- issue the platform-balance Refund (CANCELLATION only).
	//
	// For DISPUTE we do NOT call CreateRefund. When a cardholder opens a
	// chargeback the card network immediately reverses the charge and debits the
	// platform balance — Stripe surfaces this as the dispute. Calling
	// CreateRefund on a disputed charge returns charge_disputed (402) because
	// the money has already left via the chargeback, so the Refund API call would
	// error before reverseSplits ever runs, leaving the Organizer's transfer
	// un-reversed. The platform's obligation is only to claw back the Organizer
	// transfer(s) via transfer_reversal, void the tickets, and mark the Order
	// refunded.
	//
	// Design decision on the refund amount (non-DISPUTE paths):
	//   We refund the full Order.Amount (face + system/発券 fee), making the buyer
	//   whole. The processor fee (Stripe's cut) is NOT subtracted — doing so would
	//   short the buyer by an amount they never agreed to forfeit. Stripe does not
	//   return the processor fee on refunds; the platform absorbs it, which is the
	//   JP norm. Precise per-fee subtraction is intentionally deferred: it requires
	//   expanding the BalanceTransaction to obtain the exact fee.
	var providerRefundID string
	if reason != RefundReasonDispute {
		// Guard: non-Stripe orders or missing pi_ cannot be refunded via Stripe.
		if order.Payment.Provider != entity.PaymentProviderStripe {
			return nil, apperr.New(codes.FailedPrecondition,
				"refund via Stripe is only supported for Stripe-backed orders")
		}
		if order.Payment.ProviderPaymentID == "" {
			return nil, apperr.New(codes.FailedPrecondition,
				"order has no payment intent reference; cannot resolve charge for refund")
		}

		// Resolve the charge ref: prefer the value cached on the settlement row
		// (set by the payout sweep), fall back to a live PaymentIntent retrieval.
		providerChargeID, err := uc.resolveProviderChargeID(ctx, order, settlement)
		if err != nil {
			return nil, err
		}

		providerRefundID, err = uc.settlementPayouts.CreateRefund(ctx, entity.RefundParams{
			OrderID:          orderID,
			ProviderChargeID: providerChargeID,
			Amount:           order.Amount,
		})
		if err != nil {
			return nil, err
		}
	}

	// -- reverse any already-paid-out Transfer(s) per split. --
	// For Held settlements (not yet paid out) there are no transfers to reverse,
	// but we still flip the settlement to Reversed in the DB commit to prevent
	// the payout sweeper from later releasing it (TOCTOU fix).
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
	// Commits all DB mutations in one transaction:
	//   1. Settlement → Reversed (if settlement exists)
	//   2. Tickets → Voided
	//   3. Order → Refunded + provider_refund_id recorded
	var settleID entity.SettlementID
	if settlement != nil {
		settleID = settlement.ID
	}

	commit := entity.RefundCommit{
		OrderID:          orderID,
		ProviderRefundID: providerRefundID,
		SettlementID:     settleID,
		ReversedSplits:   reversedSplits,
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
		slog.String("refund_ref", providerRefundID), // empty for DISPUTE (no CreateRefund call)
	)
	return order, nil
}

// resolveProviderChargeID returns the charge ref. Prefers the value already stored on
// the settlement row (populated by the payout sweep), then falls back to a
// live PaymentIntent retrieval.
func (uc *orderUseCase) resolveProviderChargeID(ctx context.Context, order *entity.Order, settlement *entity.Settlement) (string, error) {
	if settlement != nil && settlement.ProviderChargeID != "" {
		return settlement.ProviderChargeID, nil
	}
	return uc.settlementPayouts.GetProviderChargeID(ctx, order.Payment.ProviderPaymentID)
}

// reverseSplits calls ReversePayout for every already-released split and
// returns the updated splits with ProviderPayoutReversalID populated. Splits that
// have no ProviderPayoutID (never transferred) or already have a ProviderPayoutReversalID
// (already reversed) are skipped idempotently.
func (uc *orderUseCase) reverseSplits(ctx context.Context, settlement *entity.Settlement) ([]entity.SettlementSplit, error) {
	// Work on a copy so the original settlement slice is not mutated until we
	// know all reversals succeeded.
	result := make([]entity.SettlementSplit, len(settlement.Splits))
	copy(result, settlement.Splits)

	for i := range result {
		split := &result[i]
		if split.ProviderPayoutID == "" {
			// Split was never transferred; skip.
			continue
		}
		if split.ProviderPayoutReversalID != "" {
			// Already reversed; idempotent skip.
			continue
		}
		reversalRef, err := uc.settlementPayouts.ReversePayout(ctx, entity.ReverseTransferParams{
			SettlementID:     settlement.ID,
			ProviderPayoutID: split.ProviderPayoutID,
			Amount:           split.Amount,
		})
		if err != nil {
			return nil, err
		}
		split.ProviderPayoutReversalID = reversalRef
	}
	return result, nil
}
