package rdb

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
)

// TicketEmailRepository implements entity.TicketEmailRepository for PostgreSQL.
type TicketEmailRepository struct {
	db *Database
}

const (
	ticketEmailCreateQuery = `
		INSERT INTO ticket_emails (id, user_id, event_id, email_type, raw_body, parsed_data, payment_deadline_at, lottery_start_at, lottery_end_at, application_url, lottery_result, payment_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id
	`
	ticketEmailUpdateQuery = `
		UPDATE ticket_emails
		SET payment_deadline_at = COALESCE($2, payment_deadline_at),
		    lottery_start_at = COALESCE($3, lottery_start_at),
		    lottery_end_at = COALESCE($4, lottery_end_at),
		    application_url = COALESCE($5, application_url),
		    lottery_result = COALESCE($6, lottery_result),
		    payment_status = COALESCE($7, payment_status)
		WHERE id = $1
		RETURNING id, user_id, event_id, email_type, raw_body, parsed_data, payment_deadline_at, lottery_start_at, lottery_end_at, application_url, lottery_result, payment_status
	`
	ticketEmailGetByIDQuery = `
		SELECT id, user_id, event_id, email_type, raw_body, parsed_data, payment_deadline_at, lottery_start_at, lottery_end_at, application_url, lottery_result, payment_status
		FROM ticket_emails
		WHERE id = $1
	`
	ticketEmailListByUserAndEventQuery = `
		SELECT id, user_id, event_id, email_type, raw_body, parsed_data, payment_deadline_at, lottery_start_at, lottery_end_at, application_url, lottery_result, payment_status
		FROM ticket_emails
		WHERE user_id = $1 AND event_id = $2
	`
)

// NewTicketEmailRepository creates a new ticket email repository instance.
func NewTicketEmailRepository(db *Database) *TicketEmailRepository {
	return &TicketEmailRepository{db: db}
}

// Create persists a new ticket email record and returns it with the generated ID.
func (r *TicketEmailRepository) Create(ctx context.Context, params *entity.NewTicketEmail) (*entity.TicketEmail, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, toAppErr(err, "failed to generate UUIDv7 for ticket email")
	}

	var appURL *string
	if params.ApplicationURL != "" {
		appURL = &params.ApplicationURL
	}

	_, err = r.db.Pool.Exec(ctx, ticketEmailCreateQuery,
		id.String(),
		params.UserID,
		params.EventID,
		params.EmailType,
		params.RawBody,
		params.ParsedData,
		params.PaymentDeadlineTime,
		params.LotteryStartTime,
		params.LotteryEndTime,
		appURL,
		params.LotteryResult,
		params.PaymentStatus,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to create ticket email",
			slog.String("user_id", params.UserID),
			slog.String("event_id", params.EventID),
		)
	}

	r.db.logger.Info(ctx, "ticket email created",
		slog.String("entityType", "ticket_emails"),
		slog.String("id", id.String()),
		slog.String("userID", params.UserID),
		slog.String("eventID", params.EventID),
		slog.Int("emailType", int(params.EmailType)),
	)

	return &entity.TicketEmail{
		ID:                  id.String(),
		UserID:              params.UserID,
		EventID:             params.EventID,
		EmailType:           params.EmailType,
		RawBody:             params.RawBody,
		ParsedData:          params.ParsedData,
		PaymentDeadlineTime: params.PaymentDeadlineTime,
		LotteryStartTime:    params.LotteryStartTime,
		LotteryEndTime:      params.LotteryEndTime,
		ApplicationURL:      params.ApplicationURL,
		LotteryResult:       params.LotteryResult,
		PaymentStatus:       params.PaymentStatus,
	}, nil
}

// Update applies user corrections to an existing ticket email record.
func (r *TicketEmailRepository) Update(ctx context.Context, id string, params *entity.UpdateTicketEmail) (*entity.TicketEmail, error) {
	row := r.db.Pool.QueryRow(ctx, ticketEmailUpdateQuery,
		id,
		params.PaymentDeadlineTime,
		params.LotteryStartTime,
		params.LotteryEndTime,
		params.ApplicationURL,
		params.LotteryResult,
		params.PaymentStatus,
	)

	te, err := scanTicketEmail(row)
	if err != nil {
		return nil, toAppErr(err, "failed to update ticket email", slog.String("id", id))
	}

	r.db.logger.Info(ctx, "ticket email updated",
		slog.String("entityType", "ticket_emails"),
		slog.String("id", id),
	)
	return te, nil
}

// GetByID retrieves a ticket email by its unique identifier.
func (r *TicketEmailRepository) GetByID(ctx context.Context, id string) (*entity.TicketEmail, error) {
	row := r.db.Pool.QueryRow(ctx, ticketEmailGetByIDQuery, id)

	te, err := scanTicketEmail(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get ticket email", slog.String("id", id))
	}
	return te, nil
}

// ListByUserAndEvent retrieves all ticket emails for a given user and event.
func (r *TicketEmailRepository) ListByUserAndEvent(ctx context.Context, userID, eventID string) ([]*entity.TicketEmail, error) {
	rows, err := r.db.Pool.Query(ctx, ticketEmailListByUserAndEventQuery, userID, eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list ticket emails",
			slog.String("user_id", userID),
			slog.String("event_id", eventID),
		)
	}
	defer rows.Close()

	var emails []*entity.TicketEmail
	for rows.Next() {
		te, err := scanTicketEmailFromRows(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan ticket email")
		}
		emails = append(emails, te)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating ticket email rows")
	}
	return emails, nil
}

// scannable is an interface satisfied by both pgx.Row and pgx.Rows.
type scannable interface {
	Scan(dest ...any) error
}

// scanTicketEmail scans a single row into a TicketEmail entity.
func scanTicketEmail(row scannable) (*entity.TicketEmail, error) {
	var (
		id              string
		userID          string
		eventID         string
		emailType       int16
		rawBody         string
		parsedData      json.RawMessage
		paymentDeadline *time.Time
		lotteryStart    *time.Time
		lotteryEnd      *time.Time
		applicationURL  *string
		lotteryResult   *int16
		paymentStatus   *int16
	)

	err := row.Scan(
		&id, &userID, &eventID, &emailType, &rawBody, &parsedData,
		&paymentDeadline, &lotteryStart, &lotteryEnd, &applicationURL,
		&lotteryResult, &paymentStatus,
	)
	if err != nil {
		return nil, err
	}

	te := &entity.TicketEmail{
		ID:                  id,
		UserID:              userID,
		EventID:             eventID,
		EmailType:           entity.TicketEmailType(emailType),
		RawBody:             rawBody,
		ParsedData:          parsedData,
		PaymentDeadlineTime: paymentDeadline,
		LotteryStartTime:    lotteryStart,
		LotteryEndTime:      lotteryEnd,
	}
	if applicationURL != nil {
		te.ApplicationURL = *applicationURL
	}
	if lotteryResult != nil {
		r := entity.LotteryResult(*lotteryResult)
		te.LotteryResult = &r
	}
	if paymentStatus != nil {
		s := entity.PaymentStatus(*paymentStatus)
		te.PaymentStatus = &s
	}
	return te, nil
}

// scanTicketEmailFromRows wraps scanTicketEmail for pgx.Rows (satisfies scannable).
func scanTicketEmailFromRows(rows scannable) (*entity.TicketEmail, error) {
	return scanTicketEmail(rows)
}
