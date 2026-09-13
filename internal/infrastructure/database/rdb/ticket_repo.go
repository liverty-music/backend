package rdb

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
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
		verified_identity_id, resale_without_consent_prohibited, status, issued_at
	`
	ticketListByOrderQuery  = `SELECT ` + ticketSelectColumns + ` FROM tickets WHERE order_id = $1 ORDER BY issued_at`
	ticketListByHolderQuery = `SELECT ` + ticketSelectColumns + ` FROM tickets WHERE holder_id = $1 ORDER BY issued_at DESC`
	ticketVoidByOrderQuery  = `UPDATE tickets SET status = $2 WHERE order_id = $1`
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
			t        entity.Ticket
			id       string
			orderID  string
			holderID string
			eventID  string
			viID     sql.NullString
			status   int16
		)
		if err := rows.Scan(
			&id, &orderID, &holderID, &eventID,
			&t.HolderIdentity.FullName, &t.HolderIdentity.PhoneNumber,
			&viID, &t.ResaleWithoutConsentProhibited, &status, &t.IssuedTime,
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
		tickets = append(tickets, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating ticket rows")
	}
	return tickets, nil
}
