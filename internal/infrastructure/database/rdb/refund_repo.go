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
	SET transfer_reversal_ref = $3
	WHERE settlement_id = $1 AND payee_organizer_id = $2
`

const voidTicketsByOrderTxQuery = `
	UPDATE tickets SET status = $2 WHERE order_id = $1
`

// updateOrderRefundTxQuery flips the order to Refunded and records the opaque
// provider Refund reference (re_...). For DISPUTE refunds the refund_ref is
// empty string — the column's DEFAULT ” preserves that cleanly.
const updateOrderRefundTxQuery = `
	UPDATE orders SET status = $2, refund_ref = $3 WHERE id = $1
`

// CommitRefund implements [entity.RefundRepository].
//
// All four mutations run inside one pgx transaction:
//  1. UPDATE settlements → Reversed (if SettlementID is set)
//  2. UPDATE settlement_splits with transfer_reversal_ref (per split)
//  3. UPDATE tickets → Voided
//  4. UPDATE orders → Refunded
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

	// 1. Flip settlement → Reversed (only if a settlement row exists).
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

		// 2. Persist transfer_reversal_ref on each split that was reversed.
		for _, split := range commit.ReversedSplits {
			if split.TransferReversalRef == "" {
				continue
			}
			if _, err := tx.Exec(ctx, upsertSplitReversalRefTxQuery,
				string(commit.SettlementID), split.PayeeOrganizerID, split.TransferReversalRef,
			); err != nil {
				return toAppErr(err, "failed to update split reversal ref in refund tx",
					slog.String("settlement_id", string(commit.SettlementID)),
					slog.String("payee_organizer_id", split.PayeeOrganizerID))
			}
		}
	}

	// 3. Void all tickets for the order. Idempotent: already-voided rows stay voided.
	if _, err := tx.Exec(ctx, voidTicketsByOrderTxQuery,
		string(commit.OrderID), int16(entity.TicketStatusVoided),
	); err != nil {
		return toAppErr(err, "failed to void tickets in refund tx",
			slog.String("order_id", string(commit.OrderID)))
	}

	// 4. Flip the order → Refunded and record the provider Refund ref.
	// For DISPUTE reason RefundRef is empty (no CreateRefund call was made);
	// the column DEFAULT '' handles that correctly.
	if _, err := tx.Exec(ctx, updateOrderRefundTxQuery,
		string(commit.OrderID), int16(entity.OrderStatusRefunded), commit.RefundRef,
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
