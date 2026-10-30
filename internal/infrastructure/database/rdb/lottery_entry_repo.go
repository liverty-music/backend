package rdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// LotteryEntryRepository implements [entity.LotteryEntryRepository] for
// PostgreSQL.
type LotteryEntryRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.LotteryEntryRepository = (*LotteryEntryRepository)(nil)

// NewLotteryEntryRepository creates a new LotteryEntryRepository.
func NewLotteryEntryRepository(db *Database) *LotteryEntryRepository {
	return &LotteryEntryRepository{db: db}
}

// lotteryEntrySelect reads lottery entries (aliased le) with their orders
// (aliased o), in the column order scanLotteryEntry expects; a WHERE follows.
const lotteryEntrySelect = `
	SELECT le.id, le.order_id, le.ticket_sale_id, le.state, le.draw_position, le.lost_reason,
		o.id, o.user_id, o.ticket_stock_id, o.ticket_count, o.referral_code, o.card_authorization_id,
		o.provider, o.provider_payment_id, o.provider_payment_method_id, o.card_brand, o.card_last4,
		o.status, o.amount, o.currency, o.pay_at, o.provider_refund_id, o.confirmation_send_at,
		le.id, NULL::uuid
	FROM lottery_entries le
	JOIN orders o ON o.id = le.order_id
`

// lotteryEntryCreateTimeout bounds every wait of Create's transaction: a lock
// held by a concurrent Create or an event removal, and each statement. The
// draw starts entity.LotteryDrawDelay after the end time, longer than one
// Create transaction can take.
const lotteryEntryCreateTimeout = "10s"

const (
	// lotteryEntryCreateLockQuery serializes every Create for one card
	// authorization: a transaction-scoped advisory lock keyed on its id,
	// released at commit or rollback.
	lotteryEntryCreateLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended('lottery-entry-card-authorization:' || $1, 0))`
	// lotteryEntrySaleOfStockQuery reads, for the Lottery sale $1 and the
	// stock $2, whether the sale names the stock's event; no row when either
	// does not exist or the sale is not a Lottery.
	lotteryEntrySaleOfStockQuery = `
		SELECT EXISTS (SELECT 1 FROM ticket_sale_events tse WHERE tse.ticket_sale_id = ts.id AND tse.event_id = s.event_id)
		FROM ticket_sales ts, ticket_stocks s
		WHERE ts.id = $1 AND s.id = $2 AND ts.method = 2
	`
	// lotteryEntryOfUserForEventQuery reports whether the user ($2) has an
	// entry in the sale ($1) for a stock of the event ($3).
	lotteryEntryOfUserForEventQuery = `
		SELECT EXISTS (
			SELECT 1 FROM lottery_entries le
			JOIN orders o ON o.id = le.order_id
			JOIN ticket_stocks s ON s.id = o.ticket_stock_id
			WHERE le.ticket_sale_id = $1 AND o.user_id = $2 AND s.event_id = $3
		)
	`
	// lotteryEntryInsertQuery inserts an Entered entry only while its sale's
	// end time has not passed by the database clock; no row otherwise.
	lotteryEntryInsertQuery = `
		INSERT INTO lottery_entries (id, order_id, ticket_sale_id, state)
		SELECT $1, $2, ts.id, 1 FROM ticket_sales ts
		WHERE ts.id = $3 AND clock_timestamp() < ts.sale_end_at
	`
	lotteryEntryGetQuery                      = lotteryEntrySelect + ` WHERE le.id = $1`
	lotteryEntryGetByCardAuthorizationIDQuery = lotteryEntrySelect + ` WHERE o.card_authorization_id = $1`
	// lotteryEntryListByTicketSaleAndUserQuery orders newest first: ids are
	// UUIDv7, so id order is creation order.
	lotteryEntryListByTicketSaleAndUserQuery = lotteryEntrySelect + `
		WHERE le.ticket_sale_id = $1 AND o.user_id = $2
		ORDER BY le.id DESC
	`
	lotteryEntryListByUserQuery  = lotteryEntrySelect + ` WHERE o.user_id = $1 ORDER BY le.id DESC`
	lotteryEntryListEnteredQuery = lotteryEntrySelect + `
		WHERE le.ticket_sale_id = $1 AND o.ticket_stock_id = $2 AND le.state = 1
	`
	lotteryEntryStatsQuery = `
		SELECT
			COUNT(*),
			COALESCE(SUM(o.ticket_count), 0),
			COUNT(*) FILTER (WHERE le.state = 2),
			COALESCE(SUM(o.ticket_count) FILTER (WHERE le.state = 2), 0),
			COUNT(*) FILTER (WHERE le.state = 3)
		FROM lottery_entries le
		JOIN orders o ON o.id = le.order_id
		WHERE le.ticket_sale_id = $1 AND o.ticket_stock_id = $2
	`
	lotteryEntrySaleExistsQuery = `SELECT EXISTS (SELECT 1 FROM ticket_sales WHERE id = $1)`
	// lotteryEntryLockDrawnStocksQuery takes, in id order, the row lock of
	// every stock the entries $1 are of (design D5).
	lotteryEntryLockDrawnStocksQuery = `
		SELECT 1 FROM ticket_stocks
		WHERE id IN (SELECT o.ticket_stock_id FROM lottery_entries le JOIN orders o ON o.id = le.order_id WHERE le.id = ANY($1::uuid[]))
		ORDER BY id
		FOR UPDATE
	`
	// lotteryEntrySetOutcomeQuery sets the state of every entry in $1 that is
	// still Entered to $3 with the matching draw position of $2 and lost
	// reason of $4 (NULL for winners), and returns the orders of the entries
	// it changed.
	lotteryEntrySetOutcomeQuery = `
		UPDATE lottery_entries AS le
		SET state = $3, draw_position = u.position, lost_reason = u.lost_reason
		FROM (SELECT unnest($1::uuid[]) AS id, unnest($2::bigint[]) AS position, unnest($4::smallint[]) AS lost_reason) AS u
		WHERE le.id = u.id AND le.state = 1
		RETURNING le.order_id
	`
	lotteryEntryVoidOrdersQuery = `UPDATE orders SET status = 5 WHERE id = ANY($1::uuid[]) AND status = 4`
)

// Create stores the new Entered entry with its Pending Order, or returns the
// entry whose Order already holds the card authorization, in one transaction
// under the user's row lock and the card authorization's advisory lock.
func (r *LotteryEntryRepository) Create(ctx context.Context, entry *entity.LotteryEntry) (*entity.LotteryEntry, error) {
	order := entry.Order
	attrs := []slog.Attr{
		slog.String("ticket_sale_id", string(entry.TicketSaleID)),
		slog.String("ticket_stock_id", string(order.TicketStockID)),
		slog.String("user_id", string(order.UserID)),
		slog.String("card_authorization_id", order.CardAuthorizationID),
	}
	if order.CardAuthorizationID == "" {
		return nil, apperr.New(codes.InvalidArgument, "a lottery entry is made for an order with its card authorization", attrs...)
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin lottery entry creation", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	for _, setting := range []string{"lock_timeout", "statement_timeout"} {
		if _, err := tx.Exec(ctx, "SET LOCAL "+setting+" = '"+lotteryEntryCreateTimeout+"'"); err != nil {
			return nil, toAppErr(err, "failed to bound lottery entry creation", attrs...)
		}
	}
	if err := lockUserForOrder(ctx, tx, order.UserID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, lotteryEntryCreateLockQuery, order.CardAuthorizationID); err != nil {
		return nil, toAppErr(err, "failed to lock the card authorization", attrs...)
	}
	holder, err := scanLotteryEntry(tx.QueryRow(ctx, lotteryEntryGetByCardAuthorizationIDQuery, order.CardAuthorizationID))
	switch {
	case err == nil:
		// The entry made from the card authorization already exists.
		return holder, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, toAppErr(err, "failed to read the entry of the card authorization", attrs...)
	}

	var namesEvent bool
	if err := tx.QueryRow(ctx, lotteryEntrySaleOfStockQuery, string(entry.TicketSaleID), string(order.TicketStockID)).Scan(&namesEvent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.New(codes.FailedPrecondition, "the lottery sale or the ticket stock does not exist", attrs...)
		}
		return nil, toAppErr(err, "failed to read the entry's sale", attrs...)
	}
	if !namesEvent {
		return nil, apperr.New(codes.FailedPrecondition, "the sale does not name the stock's event", attrs...)
	}
	stock, err := getTicketStock(ctx, tx, order.TicketStockID, time.Now())
	if err != nil {
		return nil, toAppErr(err, "failed to read the entry's stock", attrs...)
	}
	var entered bool
	if err := tx.QueryRow(ctx, lotteryEntryOfUserForEventQuery, string(entry.TicketSaleID), string(order.UserID), stock.EventID).Scan(&entered); err != nil {
		return nil, toAppErr(err, "failed to read the user's entries", attrs...)
	}
	if entered {
		return nil, apperr.New(codes.AlreadyExists, "the user has already entered the sale for the event", attrs...)
	}
	event, err := eventLimit(ctx, tx, stock.EventID)
	if err != nil {
		return nil, toAppErr(err, "failed to read the event's per-account limit", attrs...)
	}
	held, err := getHeldForEvent(ctx, tx, order.UserID, stock.EventID, nil, time.Now())
	if err != nil {
		return nil, toAppErr(err, "failed to count the user's tickets", attrs...)
	}
	if !event.Fits(held, stock.SeatTypeID, order.TicketCount) {
		return nil, apperr.New(codes.FailedPrecondition, "the application does not fit the event's per-account limit or seat type", attrs...)
	}

	if err := insertPendingOrder(ctx, tx, order); err != nil {
		return nil, toAppErr(err, "failed to insert the entry's order", attrs...)
	}
	tag, err := tx.Exec(ctx, lotteryEntryInsertQuery, string(entry.ID), string(order.ID), string(entry.TicketSaleID))
	if err != nil {
		return nil, toAppErr(err, "failed to insert lottery entry", attrs...)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperr.New(codes.FailedPrecondition, "the sale has ended", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit lottery entry creation", attrs...)
	}
	r.db.logger.Info(ctx, "lottery entry created",
		slog.String("entityType", "lottery_entry"),
		slog.String("lottery_entry_id", string(entry.ID)),
		slog.String("ticket_sale_id", string(entry.TicketSaleID)),
	)
	created := *entry
	created.State = entity.LotteryEntryStateEntered
	created.DrawPosition = nil
	return &created, nil
}

// Get returns the entry by its ID, in any state.
func (r *LotteryEntryRepository) Get(ctx context.Context, id entity.LotteryEntryID) (*entity.LotteryEntry, error) {
	entry, err := scanLotteryEntry(r.db.Pool.QueryRow(ctx, lotteryEntryGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get lottery entry", slog.String("lottery_entry_id", string(id)))
	}
	return entry, nil
}

// GetByCardAuthorizationID returns the entry whose order has the card
// authorization.
func (r *LotteryEntryRepository) GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*entity.LotteryEntry, error) {
	entry, err := scanLotteryEntry(r.db.Pool.QueryRow(ctx, lotteryEntryGetByCardAuthorizationIDQuery, cardAuthorizationID))
	if err != nil {
		return nil, toAppErr(err, "failed to get lottery entry by card authorization",
			slog.String("card_authorization_id", cardAuthorizationID))
	}
	return entry, nil
}

// ListByTicketSaleAndUser returns the user's entries in the sale, newest
// first.
func (r *LotteryEntryRepository) ListByTicketSaleAndUser(ctx context.Context, saleID entity.TicketSaleID, userID entity.UserID) ([]*entity.LotteryEntry, error) {
	entries, err := r.list(ctx, lotteryEntryListByTicketSaleAndUserQuery, string(saleID), string(userID))
	if err != nil {
		return nil, toAppErr(err, "failed to list lottery entries of the user",
			slog.String("ticket_sale_id", string(saleID)), slog.String("user_id", string(userID)))
	}
	return entries, nil
}

// ListByUser returns every entry of the user, newest first.
func (r *LotteryEntryRepository) ListByUser(ctx context.Context, userID entity.UserID) ([]*entity.LotteryEntry, error) {
	entries, err := r.list(ctx, lotteryEntryListByUserQuery, string(userID))
	if err != nil {
		return nil, toAppErr(err, "failed to list lottery entries of the user", slog.String("user_id", string(userID)))
	}
	return entries, nil
}

// ListEnteredByTicketSaleAndTicketStock returns every Entered entry of the
// sale for the stock.
func (r *LotteryEntryRepository) ListEnteredByTicketSaleAndTicketStock(ctx context.Context, saleID entity.TicketSaleID, stockID entity.TicketStockID) ([]*entity.LotteryEntry, error) {
	entries, err := r.list(ctx, lotteryEntryListEnteredQuery, string(saleID), string(stockID))
	if err != nil {
		return nil, toAppErr(err, "failed to list entered lottery entries",
			slog.String("ticket_sale_id", string(saleID)), slog.String("ticket_stock_id", string(stockID)))
	}
	return entries, nil
}

// GetStatsByTicketSaleAndTicketStock counts the sale's entries for the stock.
func (r *LotteryEntryRepository) GetStatsByTicketSaleAndTicketStock(ctx context.Context, saleID entity.TicketSaleID, stockID entity.TicketStockID) (entity.LotteryEntryStats, error) {
	var s entity.LotteryEntryStats
	if err := r.db.Pool.QueryRow(ctx, lotteryEntryStatsQuery, string(saleID), string(stockID)).Scan(
		&s.EntryCount, &s.TicketCount, &s.WonEntryCount, &s.WonTicketCount, &s.LostEntryCount,
	); err != nil {
		return entity.LotteryEntryStats{}, toAppErr(err, "failed to get lottery entry stats",
			slog.String("ticket_sale_id", string(saleID)), slog.String("ticket_stock_id", string(stockID)))
	}
	return s, nil
}

// PersistDrawOutcome records winners and losers, and voids the losers'
// orders, in one transaction under the row locks of the drawn stocks.
func (r *LotteryEntryRepository) PersistDrawOutcome(
	ctx context.Context,
	saleID entity.TicketSaleID,
	winners []entity.DrawWinnerRow,
	losers []entity.DrawLoserRow,
) error {
	if saleID == "" {
		return apperr.New(codes.InvalidArgument, "ticket sale ID must not be empty")
	}
	attrs := []slog.Attr{slog.String("ticket_sale_id", string(saleID))}
	for _, l := range losers {
		if l.LostReason == entity.LotteryEntryLostReasonUnspecified {
			return apperr.New(codes.InvalidArgument, "every loser needs a lost reason",
				append(attrs, slog.String("lottery_entry_id", string(l.LotteryEntryID)))...)
		}
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin draw outcome transaction", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, lotteryEntrySaleExistsQuery, string(saleID)).Scan(&exists); err != nil {
		return toAppErr(err, "failed to read the drawn sale", attrs...)
	}
	if !exists {
		return apperr.New(codes.NotFound, "ticket sale not found", attrs...)
	}
	ids := make([]string, 0, len(winners)+len(losers))
	for _, w := range winners {
		ids = append(ids, string(w.LotteryEntryID))
	}
	for _, l := range losers {
		ids = append(ids, string(l.LotteryEntryID))
	}
	if _, err := tx.Exec(ctx, lotteryEntryLockDrawnStocksQuery, ids); err != nil {
		return toAppErr(err, "failed to lock the drawn stocks", attrs...)
	}

	if len(winners) > 0 {
		ids, positions := make([]string, len(winners)), make([]int64, len(winners))
		for i, w := range winners {
			ids[i], positions[i] = string(w.LotteryEntryID), int64(w.DrawPosition)
		}
		rows, err := tx.Query(ctx, lotteryEntrySetOutcomeQuery, ids, positions, int16(entity.LotteryEntryStateWon), make([]*int16, len(winners)))
		if err != nil {
			return toAppErr(err, "failed to record the winners", append(attrs, slog.Int("winner_count", len(winners)))...)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return toAppErr(err, "failed to record the winners", attrs...)
		}
	}
	if len(losers) > 0 {
		ids, positions, reasons := make([]string, len(losers)), make([]int64, len(losers)), make([]int16, len(losers))
		for i, l := range losers {
			ids[i], positions[i], reasons[i] = string(l.LotteryEntryID), int64(l.DrawPosition), int16(l.LostReason)
		}
		rows, err := tx.Query(ctx, lotteryEntrySetOutcomeQuery, ids, positions, int16(entity.LotteryEntryStateLost), reasons)
		if err != nil {
			return toAppErr(err, "failed to record the losers", append(attrs, slog.Int("loser_count", len(losers)))...)
		}
		lostOrders, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return toAppErr(err, "failed to record the losers", attrs...)
		}
		if _, err := tx.Exec(ctx, lotteryEntryVoidOrdersQuery, lostOrders); err != nil {
			return toAppErr(err, "failed to void the losers' orders", attrs...)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit draw outcome transaction", attrs...)
	}

	r.db.logger.Info(ctx, "draw outcome persisted",
		slog.String("entityType", "ticket_sale"),
		slog.String("ticket_sale_id", string(saleID)),
		slog.Int("winner_count", len(winners)),
		slog.Int("loser_count", len(losers)),
	)
	return nil
}

// list runs a lotteryEntrySelect query and returns its rows, or an empty
// slice when there is none.
func (r *LotteryEntryRepository) list(ctx context.Context, query string, args ...any) ([]*entity.LotteryEntry, error) {
	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.LotteryEntry, error) {
		return scanLotteryEntry(row)
	})
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []*entity.LotteryEntry{}
	}
	return entries, nil
}

// scanLotteryEntry maps one row selected with lotteryEntrySelect.
// draw_position is NULL until the draw.
func scanLotteryEntry(row pgx.Row) (*entity.LotteryEntry, error) {
	var (
		e                   entity.LotteryEntry
		id, orderID, saleID string
		state               int16
		drawPosition        sql.NullInt64
		lostReason          sql.NullInt16
		orderCols           orderColumns
	)
	dest := append([]any{&id, &orderID, &saleID, &state, &drawPosition, &lostReason}, orderCols.dest()...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	e.ID = entity.LotteryEntryID(id)
	e.OrderID = entity.OrderID(orderID)
	e.TicketSaleID = entity.TicketSaleID(saleID)
	e.State = entity.LotteryEntryState(state)
	if drawPosition.Valid {
		position := int(drawPosition.Int64)
		e.DrawPosition = &position
	}
	if lostReason.Valid {
		e.LostReason = entity.LotteryEntryLostReason(lostReason.Int16)
	}
	e.Order = orderCols.order()
	return &e, nil
}
