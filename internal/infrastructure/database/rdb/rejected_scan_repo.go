package rdb

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
)

// RejectedScanRepository implements [entity.RejectedScanRepository] for
// PostgreSQL. It only ever inserts.
type RejectedScanRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.RejectedScanRepository = (*RejectedScanRepository)(nil)

// NewRejectedScanRepository creates a new RejectedScanRepository.
func NewRejectedScanRepository(db *Database) *RejectedScanRepository {
	return &RejectedScanRepository{db: db}
}

const rejectedScanInsertQuery = `
	INSERT INTO rejected_scans (id, event_id, reception_link_id, ticket_id, reason, scanned_at)
	VALUES ($1, $2, $3, $4, $5, $6)
`

// Append implements [entity.RejectedScanRepository]: every scan is validated
// first, then all are inserted in one transaction, so all or none are stored.
func (r *RejectedScanRepository) Append(ctx context.Context, scans []*entity.RejectedScan) error {
	if len(scans) == 0 {
		return nil
	}
	for _, s := range scans {
		if err := s.Validate(); err != nil {
			return err
		}
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin rejected scan append")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	batch := &pgx.Batch{}
	for _, s := range scans {
		var ticketID *string
		if s.TicketID != "" {
			id := string(s.TicketID)
			ticketID = &id
		}
		batch.Queue(rejectedScanInsertQuery, s.ID, s.EventID, string(s.ReceptionLinkID), ticketID, int16(s.Reason), s.ScannedTime)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return toAppErr(err, "failed to append rejected scans")
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit rejected scans")
	}
	return nil
}
