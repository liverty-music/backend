package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// TicketRepository implements entity.TicketRepository for PostgreSQL.
type TicketRepository struct {
	db     *Database
	outbox Announcer
}

// Compile-time interface compliance check.
var _ entity.TicketRepository = (*TicketRepository)(nil)

const (
	ticketSelectColumns = `
		id, order_id, user_id, event_id,
		identity_verification_id, resale_without_consent_prohibited, status, issue_at, admit_at
	`
	ticketListByUserQuery         = `SELECT ` + ticketSelectColumns + ` FROM tickets WHERE user_id = $1 ORDER BY issue_at DESC, id`
	ticketListByUserAndEventQuery = `SELECT ` + ticketSelectColumns + ` FROM tickets WHERE user_id = $1 AND event_id = $2 ORDER BY issue_at, id`
	ticketGetQuery                = `SELECT ` + ticketSelectColumns + ` FROM tickets WHERE id = $1`

	// ticketAdmitQuery admits one ticket in a single statement: the
	// conditional UPDATE on the ticket row is the per-ticket check-and-set (a
	// concurrent admit blocks on the row lock, then re-checks admit_at and
	// matches nothing), and the Admission is inserted from its RETURNING rows,
	// so the admitted time and the Admission are stored together or not at
	// all. The Scanner row is share-locked and must not be Revoked, so
	// a Revoke either commits first (nothing is admitted) or waits until the
	// admission is decided.
	ticketAdmitQuery = `
		WITH scanner AS (
			SELECT id FROM scanners WHERE id = $2::uuid AND status <> 3 FOR SHARE
		), admitted AS (
			UPDATE tickets SET admit_at = $3
			WHERE id = $1 AND status = 1 AND admit_at IS NULL AND EXISTS (SELECT 1 FROM scanner)
			RETURNING id, event_id, admit_at
		), recorded AS (
			INSERT INTO admissions (ticket_id, event_id, scanner_id, admitted_at)
			SELECT id, event_id, $2::uuid, admit_at FROM admitted
			RETURNING ticket_id
		)
		SELECT count(*) FROM recorded
	`

	// ticketAdmitStateQuery explains why ticketAdmitQuery admitted nothing.
	ticketAdmitStateQuery = `
		SELECT t.status, t.admit_at,
		       EXISTS (SELECT 1 FROM scanners WHERE id = $2::uuid AND status <> 3)
		FROM tickets t WHERE t.id = $1
	`
)

// NewTicketRepository creates a new ticket repository instance.
func NewTicketRepository(db *Database, outbox Announcer) *TicketRepository {
	return &TicketRepository{db: db, outbox: outbox}
}

// ListByUser returns every ticket bound to the given user, Issued and Voided
// alike, most recently issued first.
func (r *TicketRepository) ListByUser(ctx context.Context, userID entity.UserID) ([]*entity.Ticket, error) {
	rows, err := r.db.Pool.Query(ctx, ticketListByUserQuery, string(userID))
	if err != nil {
		return nil, toAppErr(err, "failed to list tickets by user", slog.String("user_id", string(userID)))
	}
	defer rows.Close()
	return scanTickets(rows)
}

// ListByUserAndEvent returns the user's tickets for the event, Issued and
// Voided alike, in issue order.
func (r *TicketRepository) ListByUserAndEvent(ctx context.Context, userID entity.UserID, eventID string) ([]*entity.Ticket, error) {
	rows, err := r.db.Pool.Query(ctx, ticketListByUserAndEventQuery, string(userID), eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list tickets by user and event",
			slog.String("user_id", string(userID)), slog.String("event_id", eventID))
	}
	defer rows.Close()
	return scanTickets(rows)
}

// Admit admits the ticket through the Scanner at admittedTime, at most once,
// storing its Admission in the same statement.
//
// # Announces
//
// ORDER.ticket_admitted with the Ticket and its admit time, in the
// transaction of the admission, when the outcome is Admitted. Nothing on
// AlreadyAdmitted or Voided.
func (r *TicketRepository) Admit(ctx context.Context, ticketID entity.TicketID, scannerID entity.ScannerID, admittedTime time.Time) (entity.AdmitResult, error) {
	attrs := []slog.Attr{slog.String("ticket_id", string(ticketID)), slog.String("scanner_id", string(scannerID))}

	admitted, err := r.admitAndAnnounce(ctx, ticketID, scannerID, admittedTime, attrs)
	if err != nil {
		return entity.AdmitResult{}, err
	}
	if admitted {
		return entity.AdmitResult{Outcome: entity.AdmitOutcomeAdmitted, AdmitTime: admittedTime}, nil
	}

	var (
		status        int16
		admittedAt    sql.NullTime
		scannerUsable bool
	)
	if err := r.db.Pool.QueryRow(ctx, ticketAdmitStateQuery, string(ticketID), string(scannerID)).Scan(&status, &admittedAt, &scannerUsable); err != nil {
		return entity.AdmitResult{}, toAppErr(err, "failed to read ticket admission state", attrs...)
	}
	switch {
	case !scannerUsable:
		return entity.AdmitResult{}, apperr.Wrap(entity.ErrScannerNotUsable, codes.PermissionDenied, "scanner not usable", attrs...)
	case admittedAt.Valid:
		return entity.AdmitResult{Outcome: entity.AdmitOutcomeAlreadyAdmitted, AdmitTime: admittedAt.Time}, nil
	case entity.TicketStatus(status) == entity.TicketStatusVoided:
		return entity.AdmitResult{Outcome: entity.AdmitOutcomeVoided}, nil
	default:
		// Unreachable: an Issued, unadmitted ticket through a usable Scanner is
		// always admitted by ticketAdmitQuery.
		return entity.AdmitResult{}, apperr.New(codes.Internal, "ticket was admissible but not admitted", attrs...)
	}
}

// admitAndAnnounce runs ticketAdmitQuery and, when it admitted the ticket,
// records ORDER.ticket_admitted with the admitted Ticket, in one
// transaction. It reports whether the ticket was admitted.
func (r *TicketRepository) admitAndAnnounce(ctx context.Context, ticketID entity.TicketID, scannerID entity.ScannerID, admittedTime time.Time, attrs []slog.Attr) (bool, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return false, toAppErr(err, "failed to begin admit transaction", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var admittedCount int
	if err := tx.QueryRow(ctx, ticketAdmitQuery, string(ticketID), string(scannerID), admittedTime).Scan(&admittedCount); err != nil {
		return false, toAppErr(err, "failed to admit ticket", attrs...)
	}
	if admittedCount != 1 {
		return false, nil
	}
	rows, err := tx.Query(ctx, ticketGetQuery, string(ticketID))
	if err != nil {
		return false, toAppErr(err, "failed to read the admitted ticket", attrs...)
	}
	tickets, err := scanTickets(rows)
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(tickets) != 1 {
		return false, apperr.New(codes.Internal, "admitted ticket not found", attrs...)
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectTicketAdmitted, messaging.Announcement{Payload: pb.FromTicket(tickets[0])}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, toAppErr(err, "failed to commit admit transaction", attrs...)
	}
	return true, nil
}

// scanTickets maps a multi-row result into a slice of entity.Ticket; the slice
// is empty, never nil, when there are no rows.
func scanTickets(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]*entity.Ticket, error) {
	tickets := []*entity.Ticket{}
	for rows.Next() {
		var (
			t                      entity.Ticket
			id                     string
			orderID                string
			userID                 string
			eventID                string
			identityVerificationID sql.NullString
			status                 int16
			admittedAt             sql.NullTime
		)
		if err := rows.Scan(
			&id, &orderID, &userID, &eventID,
			&identityVerificationID, &t.ResaleWithoutConsentProhibited, &status, &t.IssueTime, &admittedAt,
		); err != nil {
			return nil, toAppErr(err, "failed to scan ticket")
		}
		t.ID = entity.TicketID(id)
		t.OrderID = entity.OrderID(orderID)
		t.UserID = entity.UserID(userID)
		t.EventID = eventID
		if identityVerificationID.Valid {
			t.IdentityVerificationID = identityVerificationID.String
		}
		t.Status = entity.TicketStatus(status)
		if admittedAt.Valid {
			admitted := admittedAt.Time
			t.AdmitTime = &admitted
		}
		tickets = append(tickets, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating ticket rows")
	}
	return tickets, nil
}
