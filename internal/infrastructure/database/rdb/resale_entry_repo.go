package rdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// ResaleEntryRepository implements [entity.ResaleEntryRepository] for
// PostgreSQL.
type ResaleEntryRepository struct {
	db     *Database
	outbox Announcer
}

// Compile-time interface compliance check.
var _ entity.ResaleEntryRepository = (*ResaleEntryRepository)(nil)

// NewResaleEntryRepository creates a new ResaleEntryRepository.
func NewResaleEntryRepository(db *Database, outbox Announcer) *ResaleEntryRepository {
	return &ResaleEntryRepository{db: db, outbox: outbox}
}

// resaleEntrySelect reads resale entries (aliased re) with their orders
// (aliased o) and their card authorization's expiry, in the column order
// scanResaleEntry expects; a WHERE follows.
const resaleEntrySelect = `
	SELECT re.id, re.order_id, re.state, re.lost_reason, re.created_at, re.draw_at, re.withdraw_at, re.ended_email_send_at,
		ca.expire_at,
		o.id, o.user_id, o.ticket_stock_id, o.ticket_count, o.referral_code, o.card_authorization_id,
		o.provider, o.provider_payment_id, o.provider_payment_method_id, o.card_brand, o.card_last4,
		o.status, o.amount, o.currency, o.pay_at, o.provider_refund_id, o.confirmation_send_at,
		NULL::uuid, NULL::uuid, re.id
	FROM resale_entries re
	JOIN orders o ON o.id = re.order_id
	LEFT JOIN card_authorizations ca ON ca.id = o.card_authorization_id
`

// resaleEntryCreateTimeout bounds every wait of Create's transaction: a lock
// held by a concurrent Create, a draw or an event removal, and each statement.
const resaleEntryCreateTimeout = "10s"

const (
	// resaleEntryCreateLockQuery serializes every Create for one card
	// authorization: a transaction-scoped advisory lock keyed on its id.
	resaleEntryCreateLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended('resale-entry-card-authorization:' || $1, 0))`
	// resaleEntryEventStartQuery reads the start time of the event.
	resaleEntryEventStartQuery = `SELECT start_at FROM events WHERE id = $1`
	// resaleEntryEnteredForEventQuery reports whether the user ($1) has an
	// Entered resale entry for a stock of the event ($2).
	resaleEntryEnteredForEventQuery = `
		SELECT EXISTS (
			SELECT 1 FROM resale_entries re
			JOIN orders o ON o.id = re.order_id
			JOIN ticket_stocks s ON s.id = o.ticket_stock_id
			WHERE o.user_id = $1 AND s.event_id = $2 AND re.state = 1
		)
	`
	resaleEntryInsertQuery = `INSERT INTO resale_entries (id, order_id, state, created_at) VALUES ($1, $2, 1, $3)`
	// resaleEntryRecordExpiryQuery records the card authorization's expiry
	// once.
	resaleEntryRecordExpiryQuery             = `UPDATE card_authorizations SET expire_at = COALESCE(expire_at, $2) WHERE id = $1`
	resaleEntryGetQuery                      = resaleEntrySelect + ` WHERE re.id = $1`
	resaleEntryGetByCardAuthorizationIDQuery = resaleEntrySelect + ` WHERE o.card_authorization_id = $1`
	// resaleEntryListByUserQuery orders newest first: ids are UUIDv7, so id
	// order is creation order.
	resaleEntryListByUserQuery  = resaleEntrySelect + ` WHERE o.user_id = $1 ORDER BY re.id DESC`
	resaleEntryListEnteredQuery = resaleEntrySelect + ` WHERE o.ticket_stock_id = $1 AND re.state = 1`
	// resaleEntryMarkEndedEmailSentQuery sets the ended email send time once
	// and returns the row while the entry exists.
	resaleEntryMarkEndedEmailSentQuery = `
		UPDATE resale_entries SET ended_email_send_at = COALESCE(ended_email_send_at, $2)
		WHERE id = $1
		RETURNING id
	`
)

// Create stores the new Entered entry with its Pending Order, or returns the
// entry whose Order already holds the card authorization, in one transaction
// under the user's row lock and the card authorization's advisory lock
// (design D2).
func (r *ResaleEntryRepository) Create(ctx context.Context, entry *entity.ResaleEntry) (*entity.ResaleEntry, error) {
	order := entry.Order
	attrs := []slog.Attr{
		slog.String("ticket_stock_id", string(order.TicketStockID)),
		slog.String("user_id", string(order.UserID)),
		slog.String("card_authorization_id", order.CardAuthorizationID),
	}
	if order.CardAuthorizationID == "" {
		return nil, apperr.New(codes.InvalidArgument, "a resale entry is made for an order with its card authorization", attrs...)
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin resale entry creation", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	for _, setting := range []string{"lock_timeout", "statement_timeout"} {
		if _, err := tx.Exec(ctx, "SET LOCAL "+setting+" = '"+resaleEntryCreateTimeout+"'"); err != nil {
			return nil, toAppErr(err, "failed to bound resale entry creation", attrs...)
		}
	}
	if err := lockUserForOrder(ctx, tx, order.UserID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, resaleEntryCreateLockQuery, order.CardAuthorizationID); err != nil {
		return nil, toAppErr(err, "failed to lock the card authorization", attrs...)
	}
	existing, err := scanResaleEntry(tx.QueryRow(ctx, resaleEntryGetByCardAuthorizationIDQuery, order.CardAuthorizationID))
	switch {
	case err == nil:
		return existing, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, toAppErr(err, "failed to read the entry of the card authorization", attrs...)
	}

	stock, err := getTicketStock(ctx, tx, order.TicketStockID, entry.CreateTime)
	if err != nil {
		return nil, toAppErr(err, "failed to read the entry's stock", attrs...)
	}
	var startAt sql.NullTime
	if err := tx.QueryRow(ctx, resaleEntryEventStartQuery, stock.EventID).Scan(&startAt); err != nil {
		return nil, toAppErr(err, "failed to read the event's start time", attrs...)
	}
	deadline, hasDeadline := entity.ResaleDeadlineOf(&entity.Event{StartTime: optionalTime(startAt)})
	if !hasDeadline || !stock.IsResaleOpenAt(entry.CreateTime, deadline) {
		return nil, apperr.New(codes.FailedPrecondition, "the stock's resale is not open", attrs...)
	}
	var entered bool
	if err := tx.QueryRow(ctx, resaleEntryEnteredForEventQuery, string(order.UserID), stock.EventID).Scan(&entered); err != nil {
		return nil, toAppErr(err, "failed to read the user's resale entries", attrs...)
	}
	if entered {
		return nil, apperr.New(codes.AlreadyExists, "the user already has an entered resale entry for the event", attrs...)
	}
	event, err := eventLimit(ctx, tx, stock.EventID)
	if err != nil {
		return nil, toAppErr(err, "failed to read the event's per-account limit", attrs...)
	}
	held, err := getHeldForEvent(ctx, tx, order.UserID, stock.EventID, nil, entry.CreateTime)
	if err != nil {
		return nil, toAppErr(err, "failed to count the user's tickets", attrs...)
	}
	if !event.Fits(held, stock.SeatTypeID, order.TicketCount) {
		return nil, apperr.New(codes.FailedPrecondition, "the application does not fit the event's per-account limit or seat type", attrs...)
	}

	if err := insertPendingOrder(ctx, tx, order); err != nil {
		return nil, toAppErr(err, "failed to insert the entry's order", attrs...)
	}
	if _, err := tx.Exec(ctx, resaleEntryInsertQuery, string(entry.ID), string(order.ID), entry.CreateTime); err != nil {
		return nil, toAppErr(err, "failed to insert resale entry", attrs...)
	}
	if entry.CardAuthorizationExpireTime != nil {
		if _, err := tx.Exec(ctx, resaleEntryRecordExpiryQuery, order.CardAuthorizationID, *entry.CardAuthorizationExpireTime); err != nil {
			return nil, toAppErr(err, "failed to record the card authorization's expiry", attrs...)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit resale entry creation", attrs...)
	}
	r.db.logger.Info(ctx, "resale entry created",
		slog.String("entityType", "resale_entry"),
		slog.String("resale_entry_id", string(entry.ID)),
		slog.String("ticket_stock_id", string(order.TicketStockID)),
	)
	created := *entry
	created.State = entity.ResaleEntryStateEntered
	pending := *order
	pending.Status = entity.OrderStatusPending
	pending.ResaleEntryID = entry.ID
	created.Order = &pending
	return &created, nil
}

// Get returns the entry by its ID, in any state.
func (r *ResaleEntryRepository) Get(ctx context.Context, id entity.ResaleEntryID) (*entity.ResaleEntry, error) {
	entry, err := scanResaleEntry(r.db.Pool.QueryRow(ctx, resaleEntryGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get resale entry", slog.String("resale_entry_id", string(id)))
	}
	return entry, nil
}

// GetByCardAuthorizationID returns the entry whose order has the card
// authorization.
func (r *ResaleEntryRepository) GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*entity.ResaleEntry, error) {
	entry, err := scanResaleEntry(r.db.Pool.QueryRow(ctx, resaleEntryGetByCardAuthorizationIDQuery, cardAuthorizationID))
	if err != nil {
		return nil, toAppErr(err, "failed to get resale entry by card authorization",
			slog.String("card_authorization_id", cardAuthorizationID))
	}
	return entry, nil
}

// ListByUser returns the user's entries, newest first.
func (r *ResaleEntryRepository) ListByUser(ctx context.Context, userID entity.UserID) ([]*entity.ResaleEntry, error) {
	return r.list(ctx, resaleEntryListByUserQuery, string(userID), slog.String("user_id", string(userID)))
}

// ListEnteredByTicketStock returns the stock's Entered entries.
func (r *ResaleEntryRepository) ListEnteredByTicketStock(ctx context.Context, stockID entity.TicketStockID) ([]*entity.ResaleEntry, error) {
	return r.list(ctx, resaleEntryListEnteredQuery, string(stockID), slog.String("ticket_stock_id", string(stockID)))
}

// MarkEndedEmailSent sets the ended email send time once.
func (r *ResaleEntryRepository) MarkEndedEmailSent(ctx context.Context, id entity.ResaleEntryID, at time.Time) error {
	var got string
	if err := r.db.Pool.QueryRow(ctx, resaleEntryMarkEndedEmailSentQuery, string(id), at).Scan(&got); err != nil {
		return toAppErr(err, "failed to mark the resale entry's ended email sent", slog.String("resale_entry_id", string(id)))
	}
	return nil
}

// list runs a resale entry query with one argument.
func (r *ResaleEntryRepository) list(ctx context.Context, query string, arg any, attr slog.Attr) ([]*entity.ResaleEntry, error) {
	rows, err := r.db.Pool.Query(ctx, query, arg)
	if err != nil {
		return nil, toAppErr(err, "failed to list resale entries", attr)
	}
	defer rows.Close()
	entries := []*entity.ResaleEntry{}
	for rows.Next() {
		entry, err := scanResaleEntry(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan resale entry", attr)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to list resale entries", attr)
	}
	return entries, nil
}

// scanResaleEntry scans one row of resaleEntrySelect.
func scanResaleEntry(s orderScanner) (*entity.ResaleEntry, error) {
	var (
		e                                                  entity.ResaleEntry
		id, orderID                                        string
		state                                              int16
		lostReason                                         sql.NullInt16
		drawAt, withdrawAt, endedEmailSendAt, cardExpireAt sql.NullTime
		orderCols                                          orderColumns
	)
	dest := append([]any{&id, &orderID, &state, &lostReason, &e.CreateTime, &drawAt, &withdrawAt, &endedEmailSendAt, &cardExpireAt}, orderCols.dest()...)
	if err := s.Scan(dest...); err != nil {
		return nil, err
	}
	e.ID = entity.ResaleEntryID(id)
	e.OrderID = entity.OrderID(orderID)
	e.State = entity.ResaleEntryState(state)
	if lostReason.Valid {
		e.LostReason = entity.ResaleEntryLostReason(lostReason.Int16)
	}
	e.DrawTime = optionalTime(drawAt)
	e.WithdrawTime = optionalTime(withdrawAt)
	e.EndedEmailSendTime = optionalTime(endedEmailSendAt)
	e.CardAuthorizationExpireTime = optionalTime(cardExpireAt)
	e.Order = orderCols.order()
	return &e, nil
}
