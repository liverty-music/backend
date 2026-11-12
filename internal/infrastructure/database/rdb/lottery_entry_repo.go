package rdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// LotteryEntryRepository implements [entity.LotteryEntryRepository] for
// PostgreSQL.
type LotteryEntryRepository struct {
	db     *Database
	outbox Announcer
}

// Compile-time interface compliance check.
var _ entity.LotteryEntryRepository = (*LotteryEntryRepository)(nil)

// NewLotteryEntryRepository creates a new LotteryEntryRepository.
func NewLotteryEntryRepository(db *Database, outbox Announcer) *LotteryEntryRepository {
	return &LotteryEntryRepository{db: db, outbox: outbox}
}

// lotteryEntrySelect reads lottery entries (aliased le) with their orders
// (aliased o), in the column order scanLotteryEntry expects; a WHERE follows.
const lotteryEntrySelect = `
	SELECT le.id, le.order_id, le.ticket_sale_id, le.state, le.draw_position, le.lost_reason, le.loss_email_send_at,
		o.id, o.user_id, o.ticket_stock_id, o.ticket_count, o.referral_code, o.card_authorization_id,
		o.provider, o.provider_payment_id, o.provider_payment_method_id, o.card_brand, o.card_last4,
		o.status, o.amount, o.currency, o.pay_at, o.confirmation_send_at,
		orf.reason, orf.amount, orf.provider_refund_id, orf.refund_at,
		le.id, NULL::uuid, NULL::uuid
	FROM lottery_entries le
	JOIN orders o ON o.id = le.order_id
	LEFT JOIN order_refunds orf ON orf.order_id = o.id
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
	// lotteryEntryStillEnteredQuery returns the ids among $2 of the sale $1's
	// entries that are still Entered.
	lotteryEntryStillEnteredQuery = `
		SELECT le.id FROM lottery_entries le
		WHERE le.ticket_sale_id = $1 AND le.id = ANY($2::uuid[]) AND le.state = 1
	`
	// lotteryEntrySetOutcomeQuery sets the state of every entry in $1 that is
	// still Entered to $3 with the matching draw position of $2 and lost
	// reason of $4 (NULL for winners), and returns the ids and orders of the
	// entries it changed.
	lotteryEntrySetOutcomeQuery = `
		UPDATE lottery_entries AS le
		SET state = $3, draw_position = u.position, lost_reason = u.lost_reason
		FROM (SELECT unnest($1::uuid[]) AS id, unnest($2::bigint[]) AS position, unnest($4::smallint[]) AS lost_reason) AS u
		WHERE le.id = u.id AND le.state = 1
		RETURNING le.id, le.order_id
	`
	lotteryEntryVoidOrdersQuery = `UPDATE orders SET status = 5 WHERE id = ANY($1::uuid[]) AND status = 4`
	// lotteryEntryStockQuery reads the stock of the entry's Order; no row when
	// no entry has the id.
	lotteryEntryStockQuery = `SELECT o.ticket_stock_id FROM lottery_entries le JOIN orders o ON o.id = le.order_id WHERE le.id = $1`
	// lotteryEntryLockForRevertQuery locks the entry and its Order.
	lotteryEntryLockForRevertQuery = lotteryEntrySelect + ` WHERE le.id = $1 FOR UPDATE OF le, o`
	// lotteryEntryLockStocksOfEventQuery locks every stock of the event $1 in
	// id order, the order PersistDrawOutcome and RevertWin lock stocks in.
	lotteryEntryLockStocksOfEventQuery = `SELECT id FROM ticket_stocks WHERE event_id = $1 ORDER BY id FOR UPDATE`
	// lotteryEntryEndEnteredQuery makes every Entered entry whose Order is of
	// one of the stocks $1 Lost with the lost reason $2, and returns the ids,
	// orders and sales of the entries it changed.
	lotteryEntryEndEnteredQuery = `
		UPDATE lottery_entries AS le
		SET state = 3, lost_reason = $2
		FROM orders o
		WHERE o.id = le.order_id AND o.ticket_stock_id = ANY($1::uuid[]) AND le.state = 1
		RETURNING le.id, le.order_id, le.ticket_sale_id
	`
	// lotteryEntryMarkLossEmailSentQuery sets the loss email send time once
	// and returns the row while the entry exists.
	lotteryEntryMarkLossEmailSentQuery = `
		UPDATE lottery_entries SET loss_email_send_at = COALESCE(loss_email_send_at, $2)
		WHERE id = $1
		RETURNING id
	`
)

// Create stores the new Entered entry with its Pending Order, or returns the
// entry whose Order already holds the card authorization, in one transaction
// under the user's row lock and the card authorization's advisory lock.
//
// # Announces
//
// LOTTERY_ENTRY.entered with the Pending Order (which names the entry,
// the user and the TicketStock), in the transaction that stores the entry.
// Nothing when it returns an existing entry or fails.
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
	pending := *order
	pending.Status = entity.OrderStatusPending
	pending.LotteryEntryID = entry.ID
	if err := announce(ctx, r.outbox, tx, entity.SubjectLotteryEntryEntered, &pending); err != nil {
		return nil, err
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

// drawnEntry is one entry PersistDrawOutcome changed: its id and its Order.
type drawnEntry struct {
	ID      string
	OrderID string
}

// drawAnnouncementChunk bounds the announcements PersistDrawOutcome builds
// and inserts at once, so a large draw never holds them all in memory.
const drawAnnouncementChunk = 2000

// announceDrawn records subject (LOTTERY_ENTRY.won or LOTTERY_ENTRY.lost)
// for each changed entry on tx, in chunks of drawAnnouncementChunk; set
// fills in the outcome of an entry.
func (r *LotteryEntryRepository) announceDrawn(ctx context.Context, tx pgx.Tx, saleID entity.TicketSaleID, subject string, changed []drawnEntry, set func(*entity.LotteryEntry)) error {
	for start := 0; start < len(changed); start += drawAnnouncementChunk {
		end := min(start+drawAnnouncementChunk, len(changed))
		chunk := make([]*entity.LotteryEntry, 0, end-start)
		for _, c := range changed[start:end] {
			e := &entity.LotteryEntry{ID: entity.LotteryEntryID(c.ID), OrderID: entity.OrderID(c.OrderID), TicketSaleID: saleID}
			set(e)
			chunk = append(chunk, e)
		}
		if err := announceAll(ctx, r.outbox, tx, subject, chunk); err != nil {
			return err
		}
	}
	return nil
}

// drawnRow is the outcome of one entry the walk decided.
type drawnRow struct {
	id       entity.LotteryEntryID
	position int
}

// PersistDrawOutcome admits each stock's entries in draw order against the
// stock's remaining count read under its row lock, then records the winners
// and the losers and voids the losers' orders, in one transaction. An entry
// no longer Entered is left unchanged and deducts nothing.
//
// # Announces
//
// LOTTERY_ENTRY.won once per entry it sets Won and LOTTERY_ENTRY.lost once
// per entry it sets Lost, with the entry as it is after the draw, in the
// transaction of the outcome, inserted in chunks. Nothing for an entry it
// leaves unchanged.
func (r *LotteryEntryRepository) PersistDrawOutcome(ctx context.Context, saleID entity.TicketSaleID, orders []entity.StockDrawOrder) error {
	if saleID == "" {
		return apperr.New(codes.InvalidArgument, "ticket sale ID must not be empty")
	}
	attrs := []slog.Attr{slog.String("ticket_sale_id", string(saleID))}

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

	// Lock every drawn stock in id order, as every other change of a stock's
	// counts does, and read what remains of it under the lock.
	sorted := slices.Clone(orders)
	slices.SortFunc(sorted, func(a, b entity.StockDrawOrder) int {
		return strings.Compare(string(a.TicketStockID), string(b.TicketStockID))
	})
	remaining := make(map[entity.TicketStockID]int, len(sorted))
	var ids []string
	for _, o := range sorted {
		if len(o.Entries) == 0 {
			continue
		}
		stock, err := lockTicketStock(ctx, tx, o.TicketStockID, time.Now())
		if err != nil {
			return toAppErr(err, "failed to lock the drawn stock", append(attrs, slog.String("ticket_stock_id", string(o.TicketStockID)))...)
		}
		remaining[o.TicketStockID] = stock.Remaining()
		for _, e := range o.Entries {
			ids = append(ids, string(e.ID))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, lotteryEntryStillEnteredQuery, string(saleID), ids)
	if err != nil {
		return toAppErr(err, "failed to read the drawn entries", attrs...)
	}
	stillEntered, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return toAppErr(err, "failed to read the drawn entries", attrs...)
	}
	entered := make(map[entity.LotteryEntryID]bool, len(stillEntered))
	for _, id := range stillEntered {
		entered[entity.LotteryEntryID(id)] = true
	}
	decided := func(id entity.LotteryEntryID) bool { return !entered[id] }

	var winners, losers []drawnRow
	for _, o := range sorted {
		result := entity.AdmitInDrawOrder(o.Entries, remaining[o.TicketStockID], decided)
		for _, w := range result.Winners {
			winners = append(winners, drawnRow{id: w.Entry.ID, position: w.DrawPosition})
		}
		for _, l := range result.Losers {
			losers = append(losers, drawnRow{id: l.Entry.ID, position: l.DrawPosition})
		}
	}

	if err := r.recordOutcome(ctx, tx, saleID, winners, entity.LotteryEntryStateWon, entity.LotteryEntryLostReasonUnspecified); err != nil {
		return err
	}
	if err := r.recordOutcome(ctx, tx, saleID, losers, entity.LotteryEntryStateLost, entity.LotteryEntryLostReasonNotDrawn); err != nil {
		return err
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

// recordOutcome sets the drawn entries that are still Entered to state with
// their draw positions, and the lost reason for Lost, announces each one it
// changed and, for Lost, voids their Orders, on tx.
func (r *LotteryEntryRepository) recordOutcome(ctx context.Context, tx pgx.Tx, saleID entity.TicketSaleID, drawn []drawnRow, state entity.LotteryEntryState, reason entity.LotteryEntryLostReason) error {
	if len(drawn) == 0 {
		return nil
	}
	attrs := []slog.Attr{slog.String("ticket_sale_id", string(saleID)), slog.String("state", state.String()), slog.Int("entry_count", len(drawn))}
	ids, positions, reasons := make([]string, len(drawn)), make([]int64, len(drawn)), make([]*int16, len(drawn))
	byID := make(map[string]int, len(drawn))
	for i, d := range drawn {
		ids[i], positions[i] = string(d.id), int64(d.position)
		if reason != entity.LotteryEntryLostReasonUnspecified {
			v := int16(reason)
			reasons[i] = &v
		}
		byID[string(d.id)] = d.position
	}
	rows, err := tx.Query(ctx, lotteryEntrySetOutcomeQuery, ids, positions, int16(state), reasons)
	if err != nil {
		return toAppErr(err, "failed to record the draw outcome", attrs...)
	}
	changed, err := pgx.CollectRows(rows, pgx.RowToStructByPos[drawnEntry])
	if err != nil {
		return toAppErr(err, "failed to record the draw outcome", attrs...)
	}
	subject := entity.SubjectLotteryEntryWon
	if state == entity.LotteryEntryStateLost {
		subject = entity.SubjectLotteryEntryLost
	}
	if err := r.announceDrawn(ctx, tx, saleID, subject, changed, func(e *entity.LotteryEntry) {
		e.State = state
		position := byID[string(e.ID)]
		e.DrawPosition = &position
		e.LostReason = reason
	}); err != nil {
		return err
	}
	if state != entity.LotteryEntryStateLost {
		return nil
	}
	lostOrders := make([]string, len(changed))
	for i, c := range changed {
		lostOrders[i] = c.OrderID
	}
	if _, err := tx.Exec(ctx, lotteryEntryVoidOrdersQuery, lostOrders); err != nil {
		return toAppErr(err, "failed to void the losers' orders", attrs...)
	}
	return nil
}

// RevertWin makes the Won entry Lost with the lost reason and its Pending
// Order Void in one transaction under the row lock of the Order's stock, then
// of the entry and the Order.
//
// # Announces
//
// LOTTERY_ENTRY.lost with the entry Lost with the lost reason, in the
// transaction of the change. Nothing when the entry was already Lost.
func (r *LotteryEntryRepository) RevertWin(ctx context.Context, id entity.LotteryEntryID, reason entity.LotteryEntryLostReason) error {
	attrs := []slog.Attr{slog.String("lottery_entry_id", string(id)), slog.Int("lost_reason", int(reason))}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin reverting the win", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var stockID string
	if err := tx.QueryRow(ctx, lotteryEntryStockQuery, string(id)).Scan(&stockID); err != nil {
		return toAppErr(err, "failed to read the entry's stock", attrs...)
	}
	// The stock's sold count changes, so its row lock comes first (design D5).
	if _, err := lockTicketStock(ctx, tx, entity.TicketStockID(stockID), time.Now()); err != nil {
		return toAppErr(err, "failed to lock the entry's stock", attrs...)
	}
	entry, err := scanLotteryEntry(tx.QueryRow(ctx, lotteryEntryLockForRevertQuery, string(id)))
	if err != nil {
		return toAppErr(err, "failed to lock the entry", attrs...)
	}
	entryChange, orderChange, err := entry.RevertWin(reason)
	if err != nil {
		return err
	}
	if !entryChange.Changed() {
		// Already Lost: nothing changes and nothing is announced again.
		return nil
	}
	if err := applyTransition(ctx, tx, "lottery_entries", "state", string(id), entryChange, "lost_reason = $4", int16(reason)); err != nil {
		return err
	}
	if err := applyTransition(ctx, tx, "orders", "status", string(entry.OrderID), orderChange, ""); err != nil {
		return err
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectLotteryEntryLost, entry); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit reverting the win", attrs...)
	}
	r.db.logger.Info(ctx, "lottery win reverted",
		slog.String("entityType", "lottery_entry"),
		slog.String("lottery_entry_id", string(id)),
		slog.String("order_id", string(entry.OrderID)),
		slog.Int("lost_reason", int(reason)),
	)
	return nil
}

// endedEntry is one entry EndEnteredByEvent made Lost.
type endedEntry struct {
	ID           string
	OrderID      string
	TicketSaleID string
}

// EndEnteredByEvent locks the event's stocks in id order, makes their
// Entered entries Lost with the lost reason concert cancelled and voids those
// entries' Orders, in one transaction.
//
// # Announces
//
// LOTTERY_ENTRY.lost once per entry it makes Lost, with the entry as
// it is after the change, in the transaction of the change, inserted in
// chunks. Nothing when no entry changed.
func (r *LotteryEntryRepository) EndEnteredByEvent(ctx context.Context, eventID string) error {
	attrs := []slog.Attr{slog.String("event_id", eventID)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin ending the event's lottery entries", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, lotteryEntryLockStocksOfEventQuery, eventID)
	if err != nil {
		return toAppErr(err, "failed to lock the event's stocks", attrs...)
	}
	stocks, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return toAppErr(err, "failed to lock the event's stocks", attrs...)
	}
	if len(stocks) == 0 {
		return nil
	}
	reason := entity.LotteryEntryLostReasonConcertCancelled
	rows, err = tx.Query(ctx, lotteryEntryEndEnteredQuery, stocks, int16(reason))
	if err != nil {
		return toAppErr(err, "failed to end the event's lottery entries", attrs...)
	}
	ended, err := pgx.CollectRows(rows, pgx.RowToStructByPos[endedEntry])
	if err != nil {
		return toAppErr(err, "failed to end the event's lottery entries", attrs...)
	}
	if len(ended) == 0 {
		return nil
	}
	orders := make([]string, len(ended))
	for i, e := range ended {
		orders[i] = e.OrderID
	}
	if _, err := tx.Exec(ctx, lotteryEntryVoidOrdersQuery, orders); err != nil {
		return toAppErr(err, "failed to void the ended entries' orders", attrs...)
	}
	for start := 0; start < len(ended); start += drawAnnouncementChunk {
		end := min(start+drawAnnouncementChunk, len(ended))
		chunk := make([]*entity.LotteryEntry, 0, end-start)
		for _, e := range ended[start:end] {
			chunk = append(chunk, &entity.LotteryEntry{
				ID:           entity.LotteryEntryID(e.ID),
				OrderID:      entity.OrderID(e.OrderID),
				TicketSaleID: entity.TicketSaleID(e.TicketSaleID),
				State:        entity.LotteryEntryStateLost,
				LostReason:   reason,
			})
		}
		if err := announceAll(ctx, r.outbox, tx, entity.SubjectLotteryEntryLost, chunk); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit ending the event's lottery entries", attrs...)
	}
	r.db.logger.Info(ctx, "lottery entries of a cancelled event ended",
		slog.String("entityType", "lottery_entry"),
		slog.String("event_id", eventID),
		slog.Int("entry_count", len(ended)),
	)
	return nil
}

// MarkLossEmailSent sets the entry's loss email send time once.
func (r *LotteryEntryRepository) MarkLossEmailSent(ctx context.Context, id entity.LotteryEntryID, at time.Time) error {
	var got string
	if err := r.db.Pool.QueryRow(ctx, lotteryEntryMarkLossEmailSentQuery, string(id), at).Scan(&got); err != nil {
		return toAppErr(err, "failed to record the loss email", slog.String("lottery_entry_id", string(id)))
	}
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
		lossEmailSendAt     sql.NullTime
		orderCols           orderColumns
	)
	dest := append([]any{&id, &orderID, &saleID, &state, &drawPosition, &lostReason, &lossEmailSendAt}, orderCols.dest()...)
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
	if lossEmailSendAt.Valid {
		t := lossEmailSendAt.Time
		e.LossEmailSendTime = &t
	}
	e.Order = orderCols.order()
	return &e, nil
}
