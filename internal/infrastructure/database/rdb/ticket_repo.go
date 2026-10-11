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

// TicketRepository implements entity.TicketRepository for PostgreSQL.
type TicketRepository struct {
	db *Database
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

	// ticketAdmitQuery admits one ticket in a single statement: the
	// conditional UPDATE on the ticket row is the per-ticket check-and-set (a
	// concurrent admit blocks on the row lock, then re-checks admit_at and
	// matches nothing), and the Admission is inserted from its RETURNING rows,
	// so the admitted time and the Admission are stored together or not at
	// all. The reception link row is share-locked and must not be Revoked, so
	// a Revoke either commits first (nothing is admitted) or waits until the
	// admission is decided.
	ticketAdmitQuery = `
		WITH link AS (
			SELECT id FROM reception_links WHERE id = $2::uuid AND status <> 3 FOR SHARE
		), admitted AS (
			UPDATE tickets SET admit_at = $3
			WHERE id = $1 AND status = 1 AND admit_at IS NULL AND EXISTS (SELECT 1 FROM link)
			RETURNING id, event_id, admit_at
		), recorded AS (
			INSERT INTO admissions (ticket_id, event_id, reception_link_id, admitted_at)
			SELECT id, event_id, $2::uuid, admit_at FROM admitted
			RETURNING ticket_id
		)
		SELECT count(*) FROM recorded
	`

	// ticketAdmitStateQuery explains why ticketAdmitQuery admitted nothing.
	ticketAdmitStateQuery = `
		SELECT t.status, t.admit_at,
		       EXISTS (SELECT 1 FROM reception_links WHERE id = $2::uuid AND status <> 3)
		FROM tickets t WHERE t.id = $1
	`
)

// NewTicketRepository creates a new ticket repository instance.
func NewTicketRepository(db *Database) *TicketRepository {
	return &TicketRepository{db: db}
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

// Admit admits the ticket through the link at admittedTime, at most once,
// storing its Admission in the same statement.
func (r *TicketRepository) Admit(ctx context.Context, ticketID entity.TicketID, linkID entity.ReceptionLinkID, admittedTime time.Time) (entity.AdmitResult, error) {
	attrs := []slog.Attr{slog.String("ticket_id", string(ticketID)), slog.String("reception_link_id", string(linkID))}

	var admittedCount int
	if err := r.db.Pool.QueryRow(ctx, ticketAdmitQuery, string(ticketID), string(linkID), admittedTime).Scan(&admittedCount); err != nil {
		return entity.AdmitResult{}, toAppErr(err, "failed to admit ticket", attrs...)
	}
	if admittedCount == 1 {
		return entity.AdmitResult{Outcome: entity.AdmitOutcomeAdmitted, AdmitTime: admittedTime}, nil
	}

	var (
		status     int16
		admittedAt sql.NullTime
		linkUsable bool
	)
	if err := r.db.Pool.QueryRow(ctx, ticketAdmitStateQuery, string(ticketID), string(linkID)).Scan(&status, &admittedAt, &linkUsable); err != nil {
		return entity.AdmitResult{}, toAppErr(err, "failed to read ticket admission state", attrs...)
	}
	switch {
	case !linkUsable:
		return entity.AdmitResult{}, apperr.Wrap(entity.ErrReceptionLinkNotUsable, codes.PermissionDenied, "reception link not usable", attrs...)
	case admittedAt.Valid:
		return entity.AdmitResult{Outcome: entity.AdmitOutcomeAlreadyAdmitted, AdmitTime: admittedAt.Time}, nil
	case entity.TicketStatus(status) == entity.TicketStatusVoided:
		return entity.AdmitResult{Outcome: entity.AdmitOutcomeVoided}, nil
	default:
		// Unreachable: an Issued, unadmitted ticket through a usable link is
		// always admitted by ticketAdmitQuery.
		return entity.AdmitResult{}, apperr.New(codes.Internal, "ticket was admissible but not admitted", attrs...)
	}
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
