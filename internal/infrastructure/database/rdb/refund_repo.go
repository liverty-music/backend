package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// RefundRepository implements entity.RefundRepository for PostgreSQL.
// It commits all refund-related DB mutations atomically in a single pgx
// transaction so a crash between Stripe and the DB is recoverable without
// leaving the system in a split state.
type RefundRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.RefundRepository = (*RefundRepository)(nil)

// NewRefundRepository creates a new RefundRepository.
func NewRefundRepository(db *Database) *RefundRepository {
	return &RefundRepository{db: db}
}

// markReversedTxQuery transitions a settlement from Held(1) or Released(2)
// to Reversed(3). The guard rejects Unspecified(0) and an already-Reversed(3)
// settlement, preventing a loose status != 3 from accidentally flipping an
// Unspecified row and making double-reversal safe to detect via RowsAffected.
const markReversedTxQuery = `
	UPDATE settlements
	SET status = 3
	WHERE id = $1 AND status IN (1, 2)
`

const upsertSplitReversalRefTxQuery = `
	UPDATE settlement_splits
	SET provider_payout_reversal_id = $3
	WHERE settlement_id = $1 AND payee_organizer_id = $2
`

// lockTicketsByOrderTxQuery locks the order's tickets in id order. Rows are
// locked tickets first, then listings, then the settlement, as a resale
// issuance does, so the two never deadlock; and a listing being created for
// one of the tickets either commits before the cancellation below or sees the
// ticket Voided.
const lockTicketsByOrderTxQuery = `
	SELECT id FROM tickets WHERE order_id = $1 ORDER BY id FOR UPDATE
`

// voidTicketsByOrderTxQuery voids the order's Issued tickets; one already
// Voided by a resale stays as it is.
const voidTicketsByOrderTxQuery = `
	UPDATE tickets SET status = $2 WHERE order_id = $1 AND status = 1
`

// cancelListingsByOrderTxQuery ends the Listed resale listings of the order's
// tickets; a Sold listing stays as it is.
const cancelListingsByOrderTxQuery = `
	UPDATE resale_listings SET status = 4
	WHERE status = 1 AND ticket_id IN (SELECT id FROM tickets WHERE order_id = $1)
`

// updateOrderRefundTxQuery flips the order to Refunded and records the opaque
// provider Refund reference (re_...). For DISPUTE refunds the provider_refund_id is
// empty string — the column's DEFAULT ” preserves that cleanly.
const updateOrderRefundTxQuery = `
	UPDATE orders SET status = $2, provider_refund_id = $3 WHERE id = $1
`

// CommitRefund implements [entity.RefundRepository].
//
// All four mutations run inside one pgx transaction:
//  1. lock the order's tickets; UPDATE tickets → Voided (Issued ones) and
//     resale_listings → Cancelled (Listed ones)
//  2. UPDATE settlements → Reversed (if SettlementID is set)
//  3. UPDATE settlement_splits with provider_payout_reversal_id (per split)
//  4. UPDATE orders → Refunded
//
// Rows are locked tickets, listings, settlement, in the order a resale
// issuance locks them, so the two never deadlock.
//
// Idempotency: the settlement UPDATE is guarded by status IN (1, 2). If the
// settlement is already Reversed (status = 3), the UPDATE affects 0 rows and
// this function returns FailedPrecondition so the caller treats it as a
// concurrent-refund replay. The Order and ticket updates are idempotent (UPDATE
// is always a no-op when the row is already in the target status).
func (r *RefundRepository) CommitRefund(ctx context.Context, commit entity.RefundCommit) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin refund commit transaction")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Lock the order's tickets, void the Issued ones and cancel their
	// Listed listings. Idempotent: already-voided rows stay voided, and a
	// resold ticket's Sold listing is untouched.
	if _, err := tx.Exec(ctx, lockTicketsByOrderTxQuery, string(commit.OrderID)); err != nil {
		return toAppErr(err, "failed to lock tickets in refund tx",
			slog.String("order_id", string(commit.OrderID)))
	}
	if _, err := tx.Exec(ctx, voidTicketsByOrderTxQuery,
		string(commit.OrderID), int16(entity.TicketStatusVoided),
	); err != nil {
		return toAppErr(err, "failed to void tickets in refund tx",
			slog.String("order_id", string(commit.OrderID)))
	}
	if _, err := tx.Exec(ctx, cancelListingsByOrderTxQuery, string(commit.OrderID)); err != nil {
		return toAppErr(err, "failed to cancel resale listings in refund tx",
			slog.String("order_id", string(commit.OrderID)))
	}

	// 2. Flip settlement → Reversed (only if a settlement row exists).
	if commit.SettlementID != "" {
		tag, err := tx.Exec(ctx, markReversedTxQuery, string(commit.SettlementID))
		if err != nil {
			return toAppErr(err, "failed to mark settlement reversed in refund tx",
				slog.String("settlement_id", string(commit.SettlementID)))
		}
		if tag.RowsAffected() == 0 {
			// Settlement already Reversed (or Unspecified, which must not exist).
			// Signal concurrent-refund replay to the use case.
			return apperr.New(codes.FailedPrecondition,
				"settlement is already reversed; refund was applied concurrently")
		}

		// 3. Persist provider_payout_reversal_id on each split that was reversed.
		for _, split := range commit.ReversedSplits {
			if split.ProviderPayoutReversalID == "" {
				continue
			}
			if _, err := tx.Exec(ctx, upsertSplitReversalRefTxQuery,
				string(commit.SettlementID), split.PayeeOrganizerID, split.ProviderPayoutReversalID,
			); err != nil {
				return toAppErr(err, "failed to update split reversal ref in refund tx",
					slog.String("settlement_id", string(commit.SettlementID)),
					slog.String("payee_organizer_id", split.PayeeOrganizerID))
			}
		}
	}

	// 4. Flip the order → Refunded and record the provider Refund ref.
	// For DISPUTE reason ProviderRefundID is empty (no CreateRefund call was made);
	// the column DEFAULT '' handles that correctly.
	if _, err := tx.Exec(ctx, updateOrderRefundTxQuery,
		string(commit.OrderID), int16(entity.OrderStatusRefunded), commit.ProviderRefundID,
	); err != nil {
		return toAppErr(err, "failed to update order status in refund tx",
			slog.String("order_id", string(commit.OrderID)))
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit refund transaction")
	}

	r.db.logger.Info(ctx, "refund committed",
		slog.String("order_id", string(commit.OrderID)),
		slog.String("settlement_id", string(commit.SettlementID)),
	)
	return nil
}
