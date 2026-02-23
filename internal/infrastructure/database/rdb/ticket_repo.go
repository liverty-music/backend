package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// TicketRepository implements entity.TicketRepository interface.
type TicketRepository struct {
	db *Database
}

// NewTicketRepository creates a new ticket repository instance.
func NewTicketRepository(db *Database) *TicketRepository {
	return &TicketRepository{db: db}
}

const (
	insertTicketQuery = `
		INSERT INTO tickets (event_id, user_id, token_id, tx_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING id, minted_at
	`

	getTicketQuery = `
		SELECT id, event_id, user_id, token_id, tx_hash, minted_at
		FROM tickets
		WHERE id = $1
	`

	getTicketByEventAndUserQuery = `
		SELECT id, event_id, user_id, token_id, tx_hash, minted_at
		FROM tickets
		WHERE event_id = $1 AND user_id = $2
	`

	listTicketsByUserQuery = `
		SELECT id, event_id, user_id, token_id, tx_hash, minted_at
		FROM tickets
		WHERE user_id = $1
		ORDER BY minted_at DESC
	`

	listTicketsByEventQuery = `
		SELECT id, event_id, user_id, token_id, tx_hash, minted_at
		FROM tickets
		WHERE event_id = $1
		ORDER BY minted_at ASC, id ASC
	`

	eventExistsQuery = `SELECT EXISTS(SELECT 1 FROM events WHERE id = $1)`
)

// Create persists a newly minted ticket record.
func (r *TicketRepository) Create(ctx context.Context, params *entity.NewTicket) (*entity.Ticket, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	ticket := &entity.Ticket{
		EventID: params.EventID,
		UserID:  params.UserID,
		TokenID: params.TokenID,
		TxHash:  params.TxHash,
	}

	err := r.db.Pool.QueryRow(ctx, insertTicketQuery,
		ticket.EventID, ticket.UserID, ticket.TokenID, ticket.TxHash,
	).Scan(&ticket.ID, &ticket.MintTime)
	if err != nil {
		if IsUniqueViolation(err) {
			r.db.logger.Warn(ctx, "duplicate ticket",
				slog.String("entityType", "ticket"),
				slog.String("eventID", ticket.EventID),
				slog.String("userID", ticket.UserID),
			)
		}
		return nil, toAppErr(err, "failed to create ticket",
			slog.String("event_id", ticket.EventID),
			slog.String("user_id", ticket.UserID),
		)
	}

	r.db.logger.Info(ctx, "ticket created",
		slog.String("entityType", "ticket"),
		slog.String("ticketID", ticket.ID),
		slog.String("userID", ticket.UserID),
		slog.String("eventID", ticket.EventID),
	)

	return ticket, nil
}

// Get retrieves a ticket by its ID.
func (r *TicketRepository) Get(ctx context.Context, id string) (*entity.Ticket, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "ticket ID cannot be empty")
	}

	ticket := &entity.Ticket{}
	err := r.db.Pool.QueryRow(ctx, getTicketQuery, id).Scan(
		&ticket.ID, &ticket.EventID, &ticket.UserID, &ticket.TokenID, &ticket.TxHash, &ticket.MintTime,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get ticket", slog.String("ticket_id", id))
	}

	return ticket, nil
}

// GetByEventAndUser retrieves a ticket by event ID and user ID.
func (r *TicketRepository) GetByEventAndUser(ctx context.Context, eventID, userID string) (*entity.Ticket, error) {
	if eventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	if userID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	ticket := &entity.Ticket{}
	err := r.db.Pool.QueryRow(ctx, getTicketByEventAndUserQuery, eventID, userID).Scan(
		&ticket.ID, &ticket.EventID, &ticket.UserID, &ticket.TokenID, &ticket.TxHash, &ticket.MintTime,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get ticket by event and user",
			slog.String("event_id", eventID),
			slog.String("user_id", userID),
		)
	}

	return ticket, nil
}

// ListByUser retrieves all tickets for a given user, ordered by mint time descending.
func (r *TicketRepository) ListByUser(ctx context.Context, userID string) ([]*entity.Ticket, error) {
	if userID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	rows, err := r.db.Pool.Query(ctx, listTicketsByUserQuery, userID)
	if err != nil {
		return nil, toAppErr(err, "failed to list tickets for user", slog.String("user_id", userID))
	}
	defer rows.Close()

	var tickets []*entity.Ticket
	for rows.Next() {
		ticket := &entity.Ticket{}
		if err := rows.Scan(
			&ticket.ID, &ticket.EventID, &ticket.UserID, &ticket.TokenID, &ticket.TxHash, &ticket.MintTime,
		); err != nil {
			return nil, toAppErr(err, "failed to scan ticket row", slog.String("user_id", userID))
		}
		tickets = append(tickets, ticket)
	}

	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate ticket rows", slog.String("user_id", userID))
	}

	return tickets, nil
}

// ListByEvent retrieves all tickets for a given event, ordered by mint time ascending.
func (r *TicketRepository) ListByEvent(ctx context.Context, eventID string) ([]*entity.Ticket, error) {
	if eventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	rows, err := r.db.Pool.Query(ctx, listTicketsByEventQuery, eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list tickets for event", slog.String("event_id", eventID))
	}
	defer rows.Close()

	var tickets []*entity.Ticket
	for rows.Next() {
		ticket := &entity.Ticket{}
		if err := rows.Scan(
			&ticket.ID, &ticket.EventID, &ticket.UserID, &ticket.TokenID, &ticket.TxHash, &ticket.MintTime,
		); err != nil {
			return nil, toAppErr(err, "failed to scan ticket row", slog.String("event_id", eventID))
		}
		tickets = append(tickets, ticket)
	}

	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate ticket rows", slog.String("event_id", eventID))
	}

	return tickets, nil
}

// EventExists returns true if an event with the given ID exists in the database.
func (r *TicketRepository) EventExists(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	var exists bool
	err := r.db.Pool.QueryRow(ctx, eventExistsQuery, eventID).Scan(&exists)
	if err != nil {
		return false, toAppErr(err, "failed to check event existence", slog.String("event_id", eventID))
	}

	return exists, nil
}
