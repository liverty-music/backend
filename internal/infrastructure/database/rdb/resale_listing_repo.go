package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// ResaleListingRepository implements entity.ResaleListingRepository for
// PostgreSQL. A withdrawal takes the ticket stock's row lock before the
// listing's, as every change of the stock's counts does (design D5).
type ResaleListingRepository struct {
	db     *Database
	outbox Announcer
}

// Compile-time interface compliance check.
var _ entity.ResaleListingRepository = (*ResaleListingRepository)(nil)

const resaleListingColumns = `
	rl.id, rl.ticket_id, t.order_id, rl.user_id, t.event_id, rl.ticket_stock_id, rl.price, rl.status,
	rl.created_at, rl.sold_at, rl.withdraw_at, rl.provider_refund_id,
	rl.refund_at, rl.sold_email_send_at
`

const (
	resaleListingFrom     = ` FROM resale_listings rl JOIN tickets t ON t.id = rl.ticket_id `
	resaleListingGetQuery = `SELECT ` + resaleListingColumns + resaleListingFrom + `WHERE rl.id = $1`
	// resaleListingLockTicketQuery locks the ticket being listed, so a refund
	// voiding it or an admission waits for the listing (or the other way
	// round).
	resaleListingLockTicketQuery = `SELECT status, admit_at FROM tickets WHERE id = $1 FOR UPDATE`
	resaleListingInsertQuery     = `
		INSERT INTO resale_listings (id, ticket_id, user_id, ticket_stock_id, price, status, created_at)
		VALUES ($1, $2, $3, $4, $5, 1, $6)
	`
	resaleListingTicketStockQuery = `SELECT ticket_stock_id FROM resale_listings WHERE id = $1`
	// resaleListingLockedQuery reads the listing under its row lock, with its
	// event's start time for the deadline.
	resaleListingLockedQuery = `
		SELECT ` + resaleListingColumns + `, e.start_at` + resaleListingFrom + `
		JOIN events e ON e.id = t.event_id
		WHERE rl.id = $1
		FOR UPDATE OF rl
	`
	resaleListingWithdrawQuery = `UPDATE resale_listings SET status = 3, withdraw_at = $2 WHERE id = $1 AND status = 1`
	resaleListingByUserQuery   = `SELECT ` + resaleListingColumns + resaleListingFrom + `
		WHERE rl.user_id = $1
		ORDER BY rl.created_at DESC, rl.id DESC
	`
	resaleListingSoldUnrefundedQuery = `SELECT ` + resaleListingColumns + resaleListingFrom + `
		WHERE rl.status = 2 AND rl.refund_at IS NULL
		ORDER BY rl.sold_at, rl.id
	`
	resaleListingRecordRefundQuery = `
		UPDATE resale_listings SET provider_refund_id = $2, refund_at = $3
		WHERE id = $1 AND status = 2 AND refund_at IS NULL
	`
	resaleListingMarkSoldEmailSentQuery = `
		UPDATE resale_listings SET sold_email_send_at = $2
		WHERE id = $1 AND sold_email_send_at IS NULL
	`
)

// NewResaleListingRepository creates a new resale listing repository
// instance.
func NewResaleListingRepository(db *Database, outbox Announcer) *ResaleListingRepository {
	return &ResaleListingRepository{db: db, outbox: outbox}
}

// Create stores a Listed listing under the user's shared deletion lock and
// the ticket's row lock.
func (r *ResaleListingRepository) Create(ctx context.Context, listing *entity.ResaleListing) (*entity.ResaleListing, error) {
	attrs := []slog.Attr{slog.String("ticket_id", string(listing.TicketID))}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin resale listing", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status  int16
		admitAt sql.NullTime
	)
	if err := lockUserForCreate(ctx, tx, listing.UserID); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, resaleListingLockTicketQuery, string(listing.TicketID)).Scan(&status, &admitAt); err != nil {
		return nil, toAppErr(err, "failed to lock ticket", attrs...)
	}
	if entity.TicketStatus(status) != entity.TicketStatusIssued || admitAt.Valid {
		return nil, apperr.New(codes.FailedPrecondition, "the ticket is not Issued or was admitted", attrs...)
	}
	if _, err := tx.Exec(ctx, resaleListingInsertQuery,
		string(listing.ID), string(listing.TicketID), string(listing.UserID), string(listing.TicketStockID),
		listing.Price, listing.CreateTime,
	); err != nil {
		return nil, toAppErr(err, "failed to insert resale listing", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit resale listing", attrs...)
	}
	return r.Get(ctx, listing.ID)
}

// Get returns the listing.
func (r *ResaleListingRepository) Get(ctx context.Context, id entity.ResaleListingID) (*entity.ResaleListing, error) {
	l, err := scanResaleListing(r.db.Pool.QueryRow(ctx, resaleListingGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get resale listing", slog.String("resale_listing_id", string(id)))
	}
	return l, nil
}

// Withdraw makes an open listing Withdrawn under its ticket stock's row lock.
func (r *ResaleListingRepository) Withdraw(ctx context.Context, id entity.ResaleListingID, now time.Time) error {
	attrs := slog.String("resale_listing_id", string(id))
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin resale withdrawal", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var stockID string
	if err := tx.QueryRow(ctx, resaleListingTicketStockQuery, string(id)).Scan(&stockID); err != nil {
		return toAppErr(err, "failed to read the listing's ticket stock", attrs)
	}
	if _, err := tx.Exec(ctx, ticketStockLockQuery, stockID); err != nil {
		return toAppErr(err, "failed to lock ticket stock", attrs)
	}
	var startAt sql.NullTime
	l, err := scanResaleListing(tx.QueryRow(ctx, resaleListingLockedQuery, string(id)), &startAt)
	if err != nil {
		return toAppErr(err, "failed to lock resale listing", attrs)
	}
	if l.Status == entity.ResaleListingStatusWithdrawn {
		return nil
	}
	deadline, ok := entity.ResaleDeadlineOf(&entity.Event{StartTime: optionalTime(startAt)})
	if !ok || !l.IsWithdrawableAt(now, deadline) {
		return apperr.New(codes.FailedPrecondition, "the listing is sold, cancelled or expired", attrs)
	}
	if _, err := tx.Exec(ctx, resaleListingWithdrawQuery, string(id), now); err != nil {
		return toAppErr(err, "failed to withdraw resale listing", attrs)
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit resale withdrawal", attrs)
	}
	return nil
}

// ListByUser returns the user's listings, newest first.
func (r *ResaleListingRepository) ListByUser(ctx context.Context, userID entity.UserID) ([]*entity.ResaleListing, error) {
	return r.list(ctx, "failed to list the user's resale listings", resaleListingByUserQuery, string(userID))
}

// ListSoldUnrefunded returns the Sold listings not yet refunded, oldest sale
// first.
func (r *ResaleListingRepository) ListSoldUnrefunded(ctx context.Context) ([]*entity.ResaleListing, error) {
	return r.list(ctx, "failed to list sold resale listings to finish", resaleListingSoldUnrefundedQuery)
}

// RecordRefund sets the refund of a Sold listing once.
//
// # Announces
//
// ORDER.resale_listing_refunded with the ResaleListing as refunded, in the
// transaction of the refund, when it sets the refund. Nothing when the
// listing already has a refund time.
func (r *ResaleListingRepository) RecordRefund(ctx context.Context, id entity.ResaleListingID, providerRefundID string, at time.Time) error {
	attrs := slog.String("resale_listing_id", string(id))
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin resale refund", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, resaleListingRecordRefundQuery, string(id), providerRefundID, at)
	if err != nil {
		return toAppErr(err, "failed to record resale refund", attrs)
	}
	if tag.RowsAffected() == 0 {
		_ = tx.Rollback(ctx)
		current, err := r.Get(ctx, id)
		if err != nil {
			return err
		}
		if current.RefundTime != nil {
			return nil
		}
		return apperr.New(codes.FailedPrecondition, "the listing is not Sold", attrs)
	}
	refunded, err := scanResaleListing(tx.QueryRow(ctx, resaleListingGetQuery, string(id)))
	if err != nil {
		return toAppErr(err, "failed to read the refunded resale listing", attrs)
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectResaleListingRefunded, messaging.Announcement{
		Payload: pb.FromResaleListing(&usecase.ResaleListingAt{Listing: refunded, Status: refunded.Status}),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit resale refund", attrs)
	}
	return nil
}

// MarkSoldEmailSent sets the listing's sold email send time when it is unset;
// an already set time is kept.
func (r *ResaleListingRepository) MarkSoldEmailSent(ctx context.Context, id entity.ResaleListingID, at time.Time) error {
	attrs := slog.String("resale_listing_id", string(id))
	tag, err := r.db.Pool.Exec(ctx, resaleListingMarkSoldEmailSentQuery, string(id), at)
	if err != nil {
		return toAppErr(err, "failed to record the sold email", attrs)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = r.Get(ctx, id)
	return err
}

// list runs a query selecting resaleListingColumns and returns its rows.
func (r *ResaleListingRepository) list(ctx context.Context, msg, query string, args ...any) ([]*entity.ResaleListing, error) {
	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, toAppErr(err, msg)
	}
	listings, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.ResaleListing, error) {
		return scanResaleListing(row)
	})
	if err != nil {
		return nil, toAppErr(err, msg)
	}
	return listings, nil
}

// scanResaleListing maps one row selected with resaleListingColumns,
// followed by the extra destinations, if any.
func scanResaleListing(row pgx.Row, extra ...any) (*entity.ResaleListing, error) {
	var (
		l                                       entity.ResaleListing
		id, ticketID, orderID, userID, eventID  string
		stockID                                 string
		status                                  int16
		providerRefundID                        sql.NullString
		soldAt, withdrawAt, refundAt, emailedAt sql.NullTime
	)
	dest := append([]any{
		&id, &ticketID, &orderID, &userID, &eventID, &stockID, &l.Price, &status,
		&l.CreateTime, &soldAt, &withdrawAt, &providerRefundID,
		&refundAt, &emailedAt,
	}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	l.ID = entity.ResaleListingID(id)
	l.TicketID = entity.TicketID(ticketID)
	l.OrderID = entity.OrderID(orderID)
	l.UserID = entity.UserID(userID)
	l.EventID = eventID
	l.TicketStockID = entity.TicketStockID(stockID)
	l.Status = entity.ResaleListingStatus(status)
	if providerRefundID.Valid {
		l.ProviderRefundID = providerRefundID.String
	}
	l.SoldTime = optionalTime(soldAt)
	l.WithdrawTime = optionalTime(withdrawAt)
	l.RefundTime = optionalTime(refundAt)
	l.SoldEmailSendTime = optionalTime(emailedAt)
	return &l, nil
}
