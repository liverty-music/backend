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
		id, order_id, holder_id, event_id, holder_full_name, holder_phone_number,
		verified_identity_id, resale_without_consent_prohibited, status, issued_at, admitted_at
	`
	ticketListByOrderQuery          = `SELECT ` + ticketSelectColumns + ` FROM tickets WHERE order_id = $1 ORDER BY issued_at`
	ticketListByHolderQuery         = `SELECT ` + ticketSelectColumns + ` FROM tickets WHERE holder_id = $1 ORDER BY issued_at DESC`
	ticketListByHolderAndEventQuery = `SELECT ` + ticketSelectColumns + ` FROM tickets WHERE holder_id = $1 AND event_id = $2 ORDER BY issued_at, id`
	ticketVoidByOrderQuery          = `UPDATE tickets SET status = $2 WHERE order_id = $1`

	// ticketAdmitQuery admits one ticket in a single statement: the
	// conditional UPDATE on the ticket row is the per-ticket check-and-set (a
	// concurrent admit blocks on the row lock, then re-checks admitted_at and
	// matches nothing), and the Admission is inserted from its RETURNING rows,
	// so the admitted time and the Admission are stored together or not at
	// all. The reception link row is share-locked and must not be Revoked, so
	// a Revoke either commits first (nothing is admitted) or waits until the
	// admission is decided.
	ticketAdmitQuery = `
		WITH link AS (
			SELECT id FROM reception_links WHERE id = $2::uuid AND status <> 3 FOR SHARE
		), admitted AS (
			UPDATE tickets SET admitted_at = $3
			WHERE id = $1 AND status = 1 AND admitted_at IS NULL AND EXISTS (SELECT 1 FROM link)
			RETURNING id, event_id, admitted_at
		), recorded AS (
			INSERT INTO admissions (ticket_id, event_id, reception_link_id, admitted_at)
			SELECT id, event_id, $2::uuid, admitted_at FROM admitted
			RETURNING ticket_id
		)
		SELECT count(*) FROM recorded
	`

	// ticketAdmitStateQuery explains why ticketAdmitQuery admitted nothing.
	ticketAdmitStateQuery = `
		SELECT t.status, t.admitted_at,
		       EXISTS (SELECT 1 FROM reception_links WHERE id = $2::uuid AND status <> 3)
		FROM tickets t WHERE t.id = $1
	`
)

// NewTicketRepository creates a new ticket repository instance.
func NewTicketRepository(db *Database) *TicketRepository {
	return &TicketRepository{db: db}
}

// ListByOrder returns the tickets issued under the given order.
func (r *TicketRepository) ListByOrder(ctx context.Context, orderID entity.OrderID) ([]*entity.Ticket, error) {
	rows, err := r.db.Pool.Query(ctx, ticketListByOrderQuery, string(orderID))
	if err != nil {
		return nil, toAppErr(err, "failed to list tickets by order", slog.String("order_id", string(orderID)))
	}
	defer rows.Close()
	return scanTickets(rows)
}

// ListByHolder returns all tickets currently bound to the given account.
func (r *TicketRepository) ListByHolder(ctx context.Context, holderID entity.UserID) ([]*entity.Ticket, error) {
	rows, err := r.db.Pool.Query(ctx, ticketListByHolderQuery, string(holderID))
	if err != nil {
		return nil, toAppErr(err, "failed to list tickets by holder", slog.String("holder_id", string(holderID)))
	}
	defer rows.Close()
	return scanTickets(rows)
}

// ListByHolderAndEvent returns the tickets the account holds for the event,
// Issued and Voided alike, in issue order.
func (r *TicketRepository) ListByHolderAndEvent(ctx context.Context, holderID entity.UserID, eventID string) ([]*entity.Ticket, error) {
	rows, err := r.db.Pool.Query(ctx, ticketListByHolderAndEventQuery, string(holderID), eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list tickets by holder and event",
			slog.String("holder_id", string(holderID)), slog.String("event_id", eventID))
	}
	defer rows.Close()
	tickets, err := scanTickets(rows)
	if err != nil {
		return nil, err
	}
	if tickets == nil {
		tickets = []*entity.Ticket{}
	}
	return tickets, nil
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
		return entity.AdmitResult{Outcome: entity.AdmitOutcomeAdmitted, AdmittedTime: admittedTime}, nil
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
		return entity.AdmitResult{Outcome: entity.AdmitOutcomeAlreadyAdmitted, AdmittedTime: admittedAt.Time}, nil
	case entity.TicketStatus(status) == entity.TicketStatusVoided:
		return entity.AdmitResult{Outcome: entity.AdmitOutcomeVoided}, nil
	default:
		// Unreachable: an Issued, unadmitted ticket through a usable link is
		// always admitted by ticketAdmitQuery.
		return entity.AdmitResult{}, apperr.New(codes.Internal, "ticket was admissible but not admitted", attrs...)
	}
}

// VoidByOrder marks every ticket of the given order as Voided. Idempotent.
func (r *TicketRepository) VoidByOrder(ctx context.Context, orderID entity.OrderID) error {
	_, err := r.db.Pool.Exec(ctx, ticketVoidByOrderQuery, string(orderID), int16(entity.TicketStatusVoided))
	if err != nil {
		return toAppErr(err, "failed to void tickets by order", slog.String("order_id", string(orderID)))
	}
	return nil
}

// scanTickets maps a multi-row result into a slice of entity.Ticket.
func scanTickets(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]*entity.Ticket, error) {
	var tickets []*entity.Ticket
	for rows.Next() {
		var (
			t          entity.Ticket
			id         string
			orderID    string
			holderID   string
			eventID    string
			viID       sql.NullString
			status     int16
			admittedAt sql.NullTime
		)
		if err := rows.Scan(
			&id, &orderID, &holderID, &eventID,
			&t.HolderIdentity.FullName, &t.HolderIdentity.PhoneNumber,
			&viID, &t.ResaleWithoutConsentProhibited, &status, &t.IssuedTime, &admittedAt,
		); err != nil {
			return nil, toAppErr(err, "failed to scan ticket")
		}
		t.ID = entity.TicketID(id)
		t.OrderID = entity.OrderID(orderID)
		t.HolderID = entity.UserID(holderID)
		t.EventID = eventID
		if viID.Valid {
			t.VerifiedIdentityID = viID.String
		}
		t.Status = entity.TicketStatus(status)
		if admittedAt.Valid {
			admitted := admittedAt.Time
			t.AdmittedTime = &admitted
		}
		tickets = append(tickets, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating ticket rows")
	}
	return tickets, nil
}
