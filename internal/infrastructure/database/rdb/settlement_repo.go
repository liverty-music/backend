package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// SettlementRepository implements entity.SettlementRepository for PostgreSQL.
type SettlementRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.SettlementRepository = (*SettlementRepository)(nil)

// NewSettlementRepository creates a new settlement repository instance.
func NewSettlementRepository(db *Database) *SettlementRepository {
	return &SettlementRepository{db: db}
}

const (
	// upsertSettlementQuery inserts a settlement row if none exists for the
	// order_id, or returns the existing row unchanged. This ensures the sweeper
	// can call Upsert idempotently without racing to double-create rows.
	upsertSettlementQuery = `
		INSERT INTO settlements (id, order_id, organizer_id, event_id, status, settled_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (order_id) DO UPDATE
		  SET id = settlements.id
		RETURNING id, order_id, organizer_id, event_id, charge_ref, status, released_at, settled_at
	`

	getSettlementQuery = `
		SELECT id, order_id, organizer_id, event_id, charge_ref, status, released_at, settled_at
		FROM settlements WHERE id = $1
	`

	getSettlementByOrderIDQuery = `
		SELECT id, order_id, organizer_id, event_id, charge_ref, status, released_at, settled_at
		FROM settlements WHERE order_id = $1
	`

	// listHeldSettlementsQuery returns all settlements in Held status.
	// The splits are fetched separately via listSplitsBySettlementIDsQuery.
	listHeldSettlementsQuery = `
		SELECT id, order_id, organizer_id, event_id, charge_ref, status, released_at, settled_at
		FROM settlements
		WHERE status = 1
		ORDER BY settled_at
	`

	listSplitsBySettlementIDsQuery = `
		SELECT settlement_id, payee_organizer_id, amount, transfer_ref, transfer_reversal_ref
		FROM settlement_splits
		WHERE settlement_id = ANY($1::uuid[])
		ORDER BY settlement_id
	`

	// markReleasedQuery atomically transitions a Held settlement to Released,
	// records the charge_ref, sets released_at, and upserts each split's
	// transfer_ref. The status = 1 guard ensures idempotency: a concurrent
	// release attempt finds status != 1 and the UPDATE affects 0 rows, which
	// surfaces as FailedPrecondition.
	markReleasedSettlementQuery = `
		UPDATE settlements
		SET status      = 2,
		    charge_ref  = $2,
		    released_at = $3
		WHERE id = $1 AND status = 1
	`

	upsertSplitTransferRefQuery = `
		UPDATE settlement_splits
		SET transfer_ref = $3
		WHERE settlement_id = $1 AND payee_organizer_id = $2
	`
)

// Upsert implements [entity.SettlementRepository].
func (r *SettlementRepository) Upsert(ctx context.Context, s *entity.Settlement) (*entity.Settlement, error) {
	row := r.db.Pool.QueryRow(ctx, upsertSettlementQuery,
		string(s.ID),
		string(s.OrderID),
		s.OrganizerID,
		s.EventID,
		int16(s.Status),
		s.CreatedTime,
	)
	result, err := scanSettlement(row)
	if err != nil {
		return nil, toAppErr(err, "failed to upsert settlement",
			slog.String("order_id", string(s.OrderID)))
	}
	// Upsert returns the existing row; if it was just inserted its splits are
	// empty (no rows to load yet), which is correct for a new Held settlement.
	return result, nil
}

// Get implements [entity.SettlementRepository].
func (r *SettlementRepository) Get(ctx context.Context, id entity.SettlementID) (*entity.Settlement, error) {
	row := r.db.Pool.QueryRow(ctx, getSettlementQuery, string(id))
	s, err := scanSettlement(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get settlement",
			slog.String("settlement_id", string(id)))
	}
	splits, err := r.loadSplits(ctx, []string{string(id)})
	if err != nil {
		return nil, err
	}
	s.Splits = splits[string(id)]
	return s, nil
}

// GetByOrderID implements [entity.SettlementRepository].
func (r *SettlementRepository) GetByOrderID(ctx context.Context, orderID entity.OrderID) (*entity.Settlement, error) {
	row := r.db.Pool.QueryRow(ctx, getSettlementByOrderIDQuery, string(orderID))
	s, err := scanSettlement(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get settlement by order id",
			slog.String("order_id", string(orderID)))
	}
	splits, err := r.loadSplits(ctx, []string{string(s.ID)})
	if err != nil {
		return nil, err
	}
	s.Splits = splits[string(s.ID)]
	return s, nil
}

// ListHeld implements [entity.SettlementRepository].
func (r *SettlementRepository) ListHeld(ctx context.Context) ([]*entity.Settlement, error) {
	rows, err := r.db.Pool.Query(ctx, listHeldSettlementsQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list held settlements")
	}
	defer rows.Close()

	var settlements []*entity.Settlement
	var ids []string
	for rows.Next() {
		s, err := scanSettlement(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan held settlement")
		}
		settlements = append(settlements, s)
		ids = append(ids, string(s.ID))
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "held settlements iteration error")
	}

	if len(ids) == 0 {
		return settlements, nil
	}

	// Bulk-load splits for all returned settlements.
	splitsByID, err := r.loadSplits(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, s := range settlements {
		s.Splits = splitsByID[string(s.ID)]
	}
	return settlements, nil
}

// MarkReleased implements [entity.SettlementRepository].
//
// The status flip and every split's transfer_ref update run inside one pgx
// transaction, so a failure partway through (e.g. a split update violating a
// constraint) rolls back the whole operation and leaves the settlement Held —
// never Released with a split missing its transfer reference, or vice versa.
func (r *SettlementRepository) MarkReleased(ctx context.Context, id entity.SettlementID, chargeRef string, releasedAt time.Time, splits []entity.SettlementSplit) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin mark-released transaction",
			slog.String("settlement_id", string(id)))
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, markReleasedSettlementQuery,
		string(id), chargeRef, releasedAt)
	if err != nil {
		return toAppErr(err, "failed to mark settlement released",
			slog.String("settlement_id", string(id)))
	}
	if tag.RowsAffected() == 0 {
		// Either not found or status != Held — both mean we cannot mark released.
		return apperr.New(codes.FailedPrecondition,
			"settlement is not in held status; cannot mark released (already released or reversed)")
	}

	// Persist the transfer_ref for each split, in the same transaction as the
	// status flip above.
	for _, split := range splits {
		if _, err := tx.Exec(ctx, upsertSplitTransferRefQuery,
			string(id), split.PayeeOrganizerID, split.TransferRef); err != nil {
			return toAppErr(err, "failed to update split transfer ref",
				slog.String("settlement_id", string(id)),
				slog.String("payee_organizer_id", split.PayeeOrganizerID))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit mark-released transaction",
			slog.String("settlement_id", string(id)))
	}
	return nil
}

// loadSplits bulk-fetches splits for the given settlement ids.
// Returns a map of settlement_id → []SettlementSplit.
func (r *SettlementRepository) loadSplits(ctx context.Context, ids []string) (map[string][]entity.SettlementSplit, error) {
	rows, err := r.db.Pool.Query(ctx, listSplitsBySettlementIDsQuery, ids)
	if err != nil {
		return nil, toAppErr(err, "failed to load settlement splits")
	}
	defer rows.Close()

	result := make(map[string][]entity.SettlementSplit, len(ids))
	for rows.Next() {
		var (
			settlementID   string
			split          entity.SettlementSplit
			transferRef    sql.NullString
			transferRevRef sql.NullString
		)
		if err := rows.Scan(
			&settlementID,
			&split.PayeeOrganizerID,
			&split.Amount,
			&transferRef,
			&transferRevRef,
		); err != nil {
			return nil, toAppErr(err, "failed to scan settlement split")
		}
		if transferRef.Valid {
			split.TransferRef = transferRef.String
		}
		if transferRevRef.Valid {
			split.TransferReversalRef = transferRevRef.String
		}
		result[settlementID] = append(result[settlementID], split)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "settlement splits iteration error")
	}
	return result, nil
}

// settlementScanner is satisfied by both pgx.Row and pgx.Rows so scanSettlement
// works for single-row and multi-row reads.
type settlementScanner interface {
	Scan(dest ...any) error
}

// scanSettlement maps one settlements row into an entity.Settlement.
// Splits are not populated here; callers must call loadSplits separately.
func scanSettlement(s settlementScanner) (*entity.Settlement, error) {
	var (
		st         entity.Settlement
		id         string
		orderID    string
		chargeRef  sql.NullString
		releasedAt sql.NullTime
		status     int16
	)
	if err := s.Scan(
		&id,
		&orderID,
		&st.OrganizerID,
		&st.EventID,
		&chargeRef,
		&status,
		&releasedAt,
		&st.CreatedTime,
	); err != nil {
		return nil, err
	}
	st.ID = entity.SettlementID(id)
	st.OrderID = entity.OrderID(orderID)
	st.Status = entity.SettlementStatus(status)
	if chargeRef.Valid {
		st.ChargeRef = chargeRef.String
	}
	if releasedAt.Valid {
		st.ReleasedTime = releasedAt.Time
	}
	return &st, nil
}
