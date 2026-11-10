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

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
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
		o.status, o.amount, o.currency, o.pay_at, o.confirmation_send_at,
		orf.reason, orf.amount, orf.provider_refund_id, orf.refund_at,
		NULL::uuid, NULL::uuid, re.id
	FROM resale_entries re
	JOIN orders o ON o.id = re.order_id
	LEFT JOIN order_refunds orf ON orf.order_id = o.id
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
	// resaleEntryWinQuery makes the Entered entry Won at the draw time.
	resaleEntryWinQuery = `UPDATE resale_entries SET state = 2, draw_at = $2 WHERE id = $1 AND state = 1`
	// resaleEntryEndQuery makes the Entered entry Lapsed or Lost ($2) with
	// the lost reason $3 at the draw time $4 and voids its Pending Order.
	resaleEntryEndQuery = `
		WITH ended AS (
			UPDATE resale_entries SET state = $2, lost_reason = $3, draw_at = $4
			WHERE id = $1 AND state = 1
			RETURNING order_id
		)
		UPDATE orders SET status = 5 FROM ended WHERE orders.id = ended.order_id AND orders.status = 4
		RETURNING orders.id
	`
	// resaleEntryAllocateQuery allocates the listings $2 of the stock $3 to
	// the entry $1 when each is Listed and allocated to no entry or to a Lost
	// one, and returns the listings it allocated.
	resaleEntryAllocateQuery = `
		UPDATE resale_listings rl SET resale_entry_id = $1
		WHERE rl.id = ANY($2::uuid[]) AND rl.ticket_stock_id = $3 AND rl.status = 1
		  AND (rl.resale_entry_id IS NULL
		       OR EXISTS (SELECT 1 FROM resale_entries x WHERE x.id = rl.resale_entry_id AND x.state = 3))
		RETURNING rl.id
	`
	// resaleEntryStockQuery reads the stock of the entry's Order; no row when
	// no entry has the id.
	resaleEntryStockQuery = `SELECT o.ticket_stock_id FROM resale_entries re JOIN orders o ON o.id = re.order_id WHERE re.id = $1`
	// resaleEntryWithdrawQuery makes the Entered entry Withdrawn at $2.
	resaleEntryWithdrawQuery  = `UPDATE resale_entries SET state = 5, withdraw_at = $2 WHERE id = $1 AND state = 1`
	resaleEntryVoidOrderQuery = `UPDATE orders SET status = 5 WHERE id = $1 AND status = 4`
	// resaleEntryLockForRevertQuery locks the entry and its Order.
	resaleEntryLockForRevertQuery = resaleEntrySelect + ` WHERE re.id = $1 FOR UPDATE OF re, o`
	// resaleEntryRevertWinQuery makes the Won entry Lost with the lost reason
	// $2, keeping its draw time.
	resaleEntryRevertWinQuery = `UPDATE resale_entries SET state = 3, lost_reason = $2 WHERE id = $1 AND state = 2`
	// resaleEntryFreeListingsQuery lists the stock's listings free for a
	// draw, oldest first: Listed and allocated to no entry or a Lost one.
	resaleEntryFreeListingsQuery = `
		SELECT rl.id FROM resale_listings rl
		LEFT JOIN resale_entries x ON x.id = rl.resale_entry_id
		WHERE rl.ticket_stock_id = $1 AND rl.status = 1 AND (rl.resale_entry_id IS NULL OR x.state = 3)
		ORDER BY rl.created_at, rl.id
	`
	// resaleEntryStocksWithEnteredQuery lists the stocks with Entered
	// entries, with their event and its start time.
	resaleEntryStocksWithEnteredQuery = `
		SELECT DISTINCT s.id, e.id, e.start_at
		FROM resale_entries re
		JOIN orders o ON o.id = re.order_id
		JOIN ticket_stocks s ON s.id = o.ticket_stock_id
		JOIN events e ON e.id = s.event_id
		WHERE re.state = 1
	`
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

// PersistDrawOutcome records one draw of the stock under the stock's row lock.
//
// # Announces
//
// RESALE_ENTRY.won, RESALE_ENTRY.lapsed or RESALE_ENTRY.lost with each entry
// it makes Won, Lapsed or Lost, in the transaction that records the draw.
func (r *ResaleEntryRepository) PersistDrawOutcome(ctx context.Context, stockID entity.TicketStockID, drawTime time.Time, outcome entity.ResaleDrawOutcome) error {
	_, err := r.draw(ctx, stockID, drawTime, func(pgx.Tx) (entity.ResaleDrawOutcome, error) { return outcome, nil })
	return err
}

// DrawUnderLock runs one draw of the stock at drawTime in one transaction
// under the stock's row lock (design D4): it reads the stock's Entered
// entries with their card authorization's expiry and its listings free for a
// draw, oldest first, passes them to decide and records the outcome decide
// returns as PersistDrawOutcome does. Nothing a draw reads can change before
// it records, so it is never refused for a withdrawal that came first.
//
// # Announces
//
// RESALE_ENTRY.won, RESALE_ENTRY.lapsed or RESALE_ENTRY.lost with each entry
// it makes Won, Lapsed or Lost.
func (r *ResaleEntryRepository) DrawUnderLock(ctx context.Context, stockID entity.TicketStockID, drawTime time.Time, decide func(entries []*entity.ResaleEntry, freeListings []entity.ResaleListingID) entity.ResaleDrawOutcome) (entity.ResaleDrawOutcome, error) {
	return r.draw(ctx, stockID, drawTime, func(tx pgx.Tx) (entity.ResaleDrawOutcome, error) {
		entries, err := scanResaleEntries(tx.Query(ctx, resaleEntryListEnteredQuery, string(stockID)))
		if err != nil {
			return entity.ResaleDrawOutcome{}, toAppErr(err, "failed to list the draw's candidates", slog.String("ticket_stock_id", string(stockID)))
		}
		rows, err := tx.Query(ctx, resaleEntryFreeListingsQuery, string(stockID))
		if err != nil {
			return entity.ResaleDrawOutcome{}, toAppErr(err, "failed to list the free listings", slog.String("ticket_stock_id", string(stockID)))
		}
		defer rows.Close()
		var listings []entity.ResaleListingID
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return entity.ResaleDrawOutcome{}, toAppErr(err, "failed to scan a free listing")
			}
			listings = append(listings, entity.ResaleListingID(id))
		}
		if err := rows.Err(); err != nil {
			return entity.ResaleDrawOutcome{}, toAppErr(err, "failed to list the free listings")
		}
		return decide(entries, listings), nil
	})
}

// draw records, under the stock's row lock, the outcome that outcomeOf
// returns in the same transaction.
func (r *ResaleEntryRepository) draw(ctx context.Context, stockID entity.TicketStockID, drawTime time.Time, outcomeOf func(tx pgx.Tx) (entity.ResaleDrawOutcome, error)) (entity.ResaleDrawOutcome, error) {
	attrs := []slog.Attr{slog.String("ticket_stock_id", string(stockID)), slog.Time("draw_time", drawTime)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return entity.ResaleDrawOutcome{}, toAppErr(err, "failed to begin the resale draw", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, ticketStockLockQuery, string(stockID)); err != nil {
		return entity.ResaleDrawOutcome{}, toAppErr(err, "failed to lock the ticket stock", attrs...)
	}
	outcome, err := outcomeOf(tx)
	if err != nil {
		return entity.ResaleDrawOutcome{}, err
	}
	if err := func() error {
		var changed []*entity.ResaleEntry
		for _, win := range outcome.Winners {
			tag, err := tx.Exec(ctx, resaleEntryWinQuery, string(win.Entry.ID), drawTime)
			if err != nil {
				return toAppErr(err, "failed to record a resale win", attrs...)
			}
			if tag.RowsAffected() != 1 {
				return apperr.New(codes.FailedPrecondition, "a winning resale entry is no longer entered", attrs...)
			}
			ids := make([]string, len(win.Listings))
			for i, l := range win.Listings {
				ids[i] = string(l)
			}
			rows, err := tx.Query(ctx, resaleEntryAllocateQuery, string(win.Entry.ID), ids, string(stockID))
			if err != nil {
				return toAppErr(err, "failed to allocate the listings", attrs...)
			}
			allocated := 0
			for rows.Next() {
				allocated++
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return toAppErr(err, "failed to allocate the listings", attrs...)
			}
			if allocated != len(ids) {
				return apperr.New(codes.FailedPrecondition, "a listing is no longer free for a draw", attrs...)
			}
			changed = append(changed, drawn(win.Entry, entity.ResaleEntryStateWon, entity.ResaleEntryLostReasonUnspecified, drawTime))
		}
		end := func(entries []*entity.ResaleEntry, state entity.ResaleEntryState, reason entity.ResaleEntryLostReason) error {
			var lostReason any
			if state == entity.ResaleEntryStateLost {
				lostReason = int16(reason)
			}
			for _, e := range entries {
				var orderID string
				if err := tx.QueryRow(ctx, resaleEntryEndQuery, string(e.ID), int16(state), lostReason, drawTime).Scan(&orderID); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return apperr.New(codes.FailedPrecondition, "a resale entry is no longer entered", attrs...)
					}
					return toAppErr(err, "failed to end a resale entry", attrs...)
				}
				changed = append(changed, drawn(e, state, reason, drawTime))
			}
			return nil
		}
		if err := end(outcome.Lapsed, entity.ResaleEntryStateLapsed, entity.ResaleEntryLostReasonUnspecified); err != nil {
			return err
		}
		if err := end(outcome.Lost, entity.ResaleEntryStateLost, outcome.LostReason); err != nil {
			return err
		}
		for _, e := range changed {
			if err := announce(ctx, r.outbox, tx, resaleEntryDrawnSubject(e.State), messaging.Announcement{Payload: pb.FromResaleEntry(e, nil)}); err != nil {
				return err
			}
		}
		return nil
	}(); err != nil {
		return entity.ResaleDrawOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return entity.ResaleDrawOutcome{}, toAppErr(err, "failed to commit the resale draw", attrs...)
	}
	r.db.logger.Info(ctx, "resale draw recorded", append(attrs,
		slog.Int("won", len(outcome.Winners)), slog.Int("lapsed", len(outcome.Lapsed)), slog.Int("lost", len(outcome.Lost)))...)
	return outcome, nil
}

// resaleEntryDrawnSubject is the subject a draw announces an entry it made
// state with: RESALE_ENTRY.won, .lapsed or .lost.
func resaleEntryDrawnSubject(state entity.ResaleEntryState) string {
	switch state {
	case entity.ResaleEntryStateWon:
		return entity.SubjectResaleEntryWon
	case entity.ResaleEntryStateLapsed:
		return entity.SubjectResaleEntryLapsed
	default:
		return entity.SubjectResaleEntryLost
	}
}

// ListDueStocks returns, for every TicketStock with Entered resale entries,
// the stock, its event and the event's start time and publish state.
func (r *ResaleEntryRepository) ListDueStocks(ctx context.Context) ([]entity.ResaleDrawStock, error) {
	rows, err := r.db.Pool.Query(ctx, resaleEntryStocksWithEnteredQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list the stocks with resale entries")
	}
	defer rows.Close()
	var out []entity.ResaleDrawStock
	for rows.Next() {
		var (
			stockID, eventID string
			startAt          sql.NullTime
		)
		if err := rows.Scan(&stockID, &eventID, &startAt); err != nil {
			return nil, toAppErr(err, "failed to scan a stock with resale entries")
		}
		out = append(out, entity.ResaleDrawStock{StockID: entity.TicketStockID(stockID), EventID: eventID, StartTime: optionalTime(startAt)})
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to list the stocks with resale entries")
	}
	return out, nil
}

// Withdraw makes the Entered entry Withdrawn under its stock's row lock.
//
// # Announces
//
// RESALE_ENTRY.withdrawn with the Withdrawn entry, in the transaction
// that withdraws it.
func (r *ResaleEntryRepository) Withdraw(ctx context.Context, id entity.ResaleEntryID, at time.Time) (*entity.ResaleEntry, error) {
	attrs := slog.String("resale_entry_id", string(id))
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin the resale withdrawal", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var stockID string
	if err := tx.QueryRow(ctx, resaleEntryStockQuery, string(id)).Scan(&stockID); err != nil {
		return nil, toAppErr(err, "failed to read the resale entry's stock", attrs)
	}
	if _, err := tx.Exec(ctx, ticketStockLockQuery, stockID); err != nil {
		return nil, toAppErr(err, "failed to lock the ticket stock", attrs)
	}
	tag, err := tx.Exec(ctx, resaleEntryWithdrawQuery, string(id), at)
	if err != nil {
		return nil, toAppErr(err, "failed to withdraw the resale entry", attrs)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperr.New(codes.FailedPrecondition, "the resale entry is no longer waiting for a draw", attrs)
	}
	entry, err := scanResaleEntry(tx.QueryRow(ctx, resaleEntryGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to read the withdrawn resale entry", attrs)
	}
	if _, err := tx.Exec(ctx, resaleEntryVoidOrderQuery, string(entry.OrderID)); err != nil {
		return nil, toAppErr(err, "failed to void the resale entry's order", attrs)
	}
	entry.Order.Status = entity.OrderStatusVoid
	if err := announce(ctx, r.outbox, tx, entity.SubjectResaleEntryWithdrawn, messaging.Announcement{Payload: pb.FromResaleEntry(entry, nil)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit the resale withdrawal", attrs)
	}
	return entry, nil
}

// RevertWin makes a Won entry whose Order is Pending Lost under its stock's
// row lock.
//
// # Announces
//
// RESALE_ENTRY.lost with the entry as Lost, in the transaction that
// changes it. Nothing when it was already Lost.
func (r *ResaleEntryRepository) RevertWin(ctx context.Context, id entity.ResaleEntryID, reason entity.ResaleEntryLostReason) error {
	attrs := slog.String("resale_entry_id", string(id))
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin the resale revert", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var stockID string
	if err := tx.QueryRow(ctx, resaleEntryStockQuery, string(id)).Scan(&stockID); err != nil {
		return toAppErr(err, "failed to read the resale entry's stock", attrs)
	}
	if _, err := tx.Exec(ctx, ticketStockLockQuery, stockID); err != nil {
		return toAppErr(err, "failed to lock the ticket stock", attrs)
	}
	entry, err := scanResaleEntry(tx.QueryRow(ctx, resaleEntryLockForRevertQuery, string(id)))
	if err != nil {
		return toAppErr(err, "failed to lock the resale entry", attrs)
	}
	if entry.State == entity.ResaleEntryStateLost {
		// Already Lost: nothing changes and nothing is announced again.
		return nil
	}
	if err := entry.RevertWin(reason); err != nil {
		return apperr.Wrap(err, codes.FailedPrecondition, "the resale entry is not a won entry with a pending order", attrs)
	}
	if _, err := tx.Exec(ctx, resaleEntryRevertWinQuery, string(id), int16(reason)); err != nil {
		return toAppErr(err, "failed to make the resale entry lost", attrs)
	}
	if _, err := tx.Exec(ctx, resaleEntryVoidOrderQuery, string(entry.OrderID)); err != nil {
		return toAppErr(err, "failed to void the resale entry's order", attrs)
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectResaleEntryLost, messaging.Announcement{Payload: pb.FromResaleEntry(entry, nil)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit the resale revert", attrs)
	}
	return nil
}

// drawn returns a copy of the entry as a draw at drawTime leaves it.
func drawn(e *entity.ResaleEntry, state entity.ResaleEntryState, reason entity.ResaleEntryLostReason, drawTime time.Time) *entity.ResaleEntry {
	copied := *e
	copied.State = state
	copied.DrawTime = &drawTime
	if state == entity.ResaleEntryStateLost {
		copied.LostReason = reason
	}
	if copied.Order != nil && state != entity.ResaleEntryStateWon {
		order := *copied.Order
		order.Status = entity.OrderStatusVoid
		copied.Order = &order
	}
	return &copied
}

// list runs a resale entry query with one argument.
func (r *ResaleEntryRepository) list(ctx context.Context, query string, arg any, attr slog.Attr) ([]*entity.ResaleEntry, error) {
	entries, err := scanResaleEntries(r.db.Pool.Query(ctx, query, arg))
	if err != nil {
		return nil, toAppErr(err, "failed to list resale entries", attr)
	}
	return entries, nil
}

// scanResaleEntries scans the rows of a resale entry query.
func scanResaleEntries(rows pgx.Rows, err error) ([]*entity.ResaleEntry, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []*entity.ResaleEntry{}
	for rows.Next() {
		entry, err := scanResaleEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
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
