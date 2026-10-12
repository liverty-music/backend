package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// AdmissionRepository implements [entity.AdmissionRepository] for PostgreSQL.
// Admissions are written only by [TicketRepository.Admit].
type AdmissionRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.AdmissionRepository = (*AdmissionRepository)(nil)

// NewAdmissionRepository creates a new AdmissionRepository.
func NewAdmissionRepository(db *Database) *AdmissionRepository {
	return &AdmissionRepository{db: db}
}

const admissionGetByTicketQuery = `
	SELECT a.ticket_id, a.event_id, a.scanner_id, l.number, a.admitted_at
	FROM admissions a
	JOIN scanners l ON l.id = a.scanner_id
	WHERE a.ticket_id = $1
`

// GetByTicket implements [entity.AdmissionRepository].
func (r *AdmissionRepository) GetByTicket(ctx context.Context, ticketID entity.TicketID) (*entity.Admission, error) {
	var (
		a         entity.Admission
		ticketDB  string
		scannerID string
	)
	if err := r.db.Pool.QueryRow(ctx, admissionGetByTicketQuery, string(ticketID)).
		Scan(&ticketDB, &a.EventID, &scannerID, &a.ScannerNumber, &a.AdmitTime); err != nil {
		return nil, toAppErr(err, "failed to get admission", slog.String("ticket_id", string(ticketID)))
	}
	a.TicketID = entity.TicketID(ticketDB)
	a.ScannerID = entity.ScannerID(scannerID)
	return &a, nil
}
