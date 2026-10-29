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

// CheckoutRepository implements entity.CheckoutRepository for PostgreSQL. A
// Checkout is stored with its Order. Every write that changes a stock's sold
// or held count takes the user's users row lock (a start only) and then the
// stock's row lock before the checkout and order rows, so the writes of one
// stock never deadlock (design D4, D5).
type CheckoutRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.CheckoutRepository = (*CheckoutRepository)(nil)

// checkoutSelect reads checkouts (aliased c) with their orders (aliased o), in
// the column order scanCheckout expects; a WHERE on c or o follows.
const checkoutSelect = `
	SELECT c.id, c.order_id, c.ticket_sale_id, c.card_authorization_release_at, c.status,
		c.hold_expire_at, c.commit_at, c.capture_at,
		o.id, o.user_id, o.ticket_stock_id, o.ticket_count, o.referral_code, o.card_authorization_id,
		o.provider, o.provider_payment_id, o.provider_payment_method_id, o.card_brand, o.card_last4,
		o.status, o.amount, o.currency, o.pay_at, o.provider_refund_id, o.confirmation_send_at,
		NULL::uuid, c.id
	FROM checkouts c
	JOIN orders o ON o.id = c.order_id
`

const (
	checkoutGetQuery                      = checkoutSelect + ` WHERE c.id = $1`
	checkoutGetByCardAuthorizationIDQuery = checkoutSelect + ` WHERE o.card_authorization_id = $1`
	// checkoutListByUserQuery orders newest first by the UUIDv7 id.
	checkoutListByUserQuery = checkoutSelect + ` WHERE o.user_id = $1 ORDER BY c.id DESC`
	// checkoutHeldOfUserQuery locks the user's Held checkouts of the stock,
	// lapsed or not; GetOrCreateHeld keeps them to at most one.
	checkoutHeldOfUserQuery = checkoutSelect + `
		WHERE o.user_id = $1 AND o.ticket_stock_id = $2 AND c.status = 1
		ORDER BY c.id
		FOR UPDATE OF c
	`
	// checkoutUserLockQuery takes the user's row lock, which every creation of
	// an Order by the user takes first (design D4).
	checkoutUserLockQuery   = `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`
	checkoutEventLimitQuery = `SELECT per_account_limit FROM events WHERE id = $1`
	checkoutEndHeldQuery    = `UPDATE checkouts SET status = $2 WHERE id = $1 AND status = 1`
	// orderVoidQuery makes a Pending order Void.
	orderVoidQuery          = `UPDATE orders SET status = 5 WHERE id = $1 AND status = 4`
	orderInsertPendingQuery = `
		INSERT INTO orders (
			id, user_id, ticket_stock_id, ticket_count, referral_code, card_authorization_id,
			provider, status, amount, currency
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 4, $8, $9)
	`
	checkoutInsertQuery = `
		INSERT INTO checkouts (id, order_id, ticket_sale_id, status, hold_expire_at)
		VALUES ($1, $2, $3, 1, $4)
	`
	checkoutSetCardAuthorizationQuery = `
		UPDATE orders o SET card_authorization_id = $2
		FROM checkouts c
		WHERE c.order_id = o.id AND c.id = $1 AND c.status = 1
		  AND (o.card_authorization_id IS NULL OR o.card_authorization_id = $2)
	`
	checkoutCommitQuery = `
		UPDATE checkouts SET status = 2, commit_at = $2
		WHERE id = $1 AND status = 1 AND hold_expire_at > $2
	`
	checkoutReleaseQuery = `
		UPDATE checkouts SET status = 4
		WHERE id = $1 AND status = 1 AND hold_expire_at <= $2
		RETURNING order_id
	`
	checkoutRevertCommitQuery = `
		UPDATE checkouts SET status = 5
		WHERE id = $1 AND status = 2 AND capture_at IS NULL
		RETURNING order_id
	`
	checkoutRecordCaptureQuery = `
		UPDATE checkouts SET capture_at = $2
		WHERE id = $1 AND status = 2 AND capture_at IS NULL
	`
	checkoutRecordCardAuthorizationReleaseQuery = `
		UPDATE checkouts c SET card_authorization_release_at = $2
		FROM orders o
		WHERE o.id = c.order_id AND c.id = $1 AND c.status IN (4, 5)
		  AND o.card_authorization_id IS NOT NULL AND c.card_authorization_release_at IS NULL
	`
	checkoutListDueQuery = checkoutSelect + `
		WHERE (c.status = 1 AND c.hold_expire_at <= $1)
		   OR (c.status IN (4, 5) AND o.card_authorization_id IS NOT NULL AND c.card_authorization_release_at IS NULL)
		   OR (c.status = 2 AND c.commit_at < $2)
		ORDER BY c.hold_expire_at, c.id
	`
	// checkoutAdvisoryLockQuery takes a transaction-scoped advisory lock
	// keyed on the checkout id; it is released at commit or rollback, and
	// when the session ends.
	checkoutAdvisoryLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended('checkout:' || $1, 0))`
)

// NewCheckoutRepository creates a new checkout repository instance.
func NewCheckoutRepository(db *Database) *CheckoutRepository {
	return &CheckoutRepository{db: db}
}

// lockUserForOrder takes the user's deletion lock shared and the user's row
// lock for the rest of tx, so the user's Order creations run one after the
// other and none outlives a deletion of the user.
func lockUserForOrder(ctx context.Context, tx pgx.Tx, userID entity.UserID) error {
	if err := lockUserForCreate(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, checkoutUserLockQuery, string(userID)); err != nil {
		return toAppErr(err, "failed to lock the user", slog.String("user_id", string(userID)))
	}
	return nil
}

// eventLimit reads the event's per-account limit in tx.
func eventLimit(ctx context.Context, tx pgx.Tx, eventID string) (*entity.Event, error) {
	var limit sql.NullInt32
	if err := tx.QueryRow(ctx, checkoutEventLimitQuery, eventID).Scan(&limit); err != nil {
		return nil, err
	}
	return &entity.Event{ID: eventID, PerAccountLimit: optionalInt(limit)}, nil
}

// optionalInt returns nil for a NULL integer column, or its value.
func optionalInt(v sql.NullInt32) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int32)
	return &i
}

// insertPendingOrder stores a Pending order in tx.
func insertPendingOrder(ctx context.Context, tx pgx.Tx, o *entity.Order) error {
	var code, cardAuthorizationID any
	if o.ReferralCode != nil {
		code = string(*o.ReferralCode)
	}
	if o.CardAuthorizationID != "" {
		cardAuthorizationID = o.CardAuthorizationID
	}
	_, err := tx.Exec(ctx, orderInsertPendingQuery,
		string(o.ID), string(o.UserID), string(o.TicketStockID), o.TicketCount, code, cardAuthorizationID,
		int16(o.Payment.Provider), o.Amount, o.Currency,
	)
	return err
}

// GetOrCreateHeld returns the user's holding checkout of the sale for the
// stock with the same count, or creates a new Held one with its Pending Order
// under the user's row lock and the stock's row lock.
func (r *CheckoutRepository) GetOrCreateHeld(ctx context.Context, stockID entity.TicketStockID, saleID entity.TicketSaleID, userID entity.UserID, count int, referralCode *entity.ReferralCode, now time.Time) (*entity.Checkout, error) {
	attrs := []slog.Attr{slog.String("ticket_stock_id", string(stockID)), slog.String("user_id", string(userID))}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin checkout start", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockUserForOrder(ctx, tx, userID); err != nil {
		return nil, err
	}
	stock, err := lockTicketStock(ctx, tx, stockID, now)
	if err != nil {
		return nil, toAppErr(err, "failed to lock ticket stock", attrs...)
	}

	held, err := r.heldOfUser(ctx, tx, userID, stockID)
	if err != nil {
		return nil, toAppErr(err, "failed to read the user's held checkout", attrs...)
	}
	var holding *entity.Checkout
	for _, c := range held {
		if c.IsHoldingAt(now) {
			holding = c
		}
	}
	if holding != nil && holding.TicketSaleID == saleID && holding.Order.TicketCount == count {
		return holding, nil
	}

	// The user's own holding checkout counts as free: it is replaced. The
	// count is taken off the stockHeld count before the remaining count is
	// computed, so a stock already over its quantity frees nothing.
	stockHeld := stock.HeldCount
	var except *entity.OrderID
	if holding != nil {
		stockHeld -= holding.Order.TicketCount
		except = &holding.OrderID
	}
	free := stock.Quantity - stock.SoldCount - stockHeld
	if free < count {
		return nil, apperr.New(codes.ResourceExhausted, "not enough tickets remain", attrs...)
	}
	event, err := eventLimit(ctx, tx, stock.EventID)
	if err != nil {
		return nil, toAppErr(err, "failed to read the event's per-account limit", attrs...)
	}
	tickets, err := getHeldForEvent(ctx, tx, userID, stock.EventID, except, now)
	if err != nil {
		return nil, toAppErr(err, "failed to count the user's tickets", attrs...)
	}
	if !event.Fits(tickets, stock.SeatTypeID, count) {
		return nil, apperr.New(codes.FailedPrecondition, "the application does not fit the event's per-account limit or seat type", attrs...)
	}

	for _, c := range held {
		next := entity.CheckoutStatusExpired
		if c.IsHoldingAt(now) {
			next = entity.CheckoutStatusReleased
		}
		if _, err := tx.Exec(ctx, checkoutEndHeldQuery, string(c.ID), int16(next)); err != nil {
			return nil, toAppErr(err, "failed to end the replaced checkout", attrs...)
		}
		if _, err := tx.Exec(ctx, orderVoidQuery, string(c.OrderID)); err != nil {
			return nil, toAppErr(err, "failed to void the replaced checkout's order", attrs...)
		}
	}

	order := entity.NewPendingOrder(userID, stock, count, referralCode, entity.OrderPaymentProvider)
	checkout := entity.NewCheckout(order, saleID, now)
	if err := insertPendingOrder(ctx, tx, order); err != nil {
		return nil, toAppErr(err, "failed to insert the checkout's order", attrs...)
	}
	if _, err := tx.Exec(ctx, checkoutInsertQuery, string(checkout.ID), string(order.ID), string(saleID), checkout.HoldExpireTime); err != nil {
		return nil, toAppErr(err, "failed to insert checkout", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit checkout start", attrs...)
	}
	return checkout, nil
}

// heldOfUser locks and returns the user's Held checkouts of the stock.
func (r *CheckoutRepository) heldOfUser(ctx context.Context, tx pgx.Tx, userID entity.UserID, stockID entity.TicketStockID) ([]*entity.Checkout, error) {
	rows, err := tx.Query(ctx, checkoutHeldOfUserQuery, string(userID), string(stockID))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.Checkout, error) { return scanCheckout(row) })
}

// Get returns the checkout with the given id.
func (r *CheckoutRepository) Get(ctx context.Context, id entity.CheckoutID) (*entity.Checkout, error) {
	c, err := scanCheckout(r.db.Pool.QueryRow(ctx, checkoutGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get checkout", slog.String("checkout_id", string(id)))
	}
	return c, nil
}

// GetByCardAuthorizationID returns the checkout whose order has the given card
// authorization.
func (r *CheckoutRepository) GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*entity.Checkout, error) {
	c, err := scanCheckout(r.db.Pool.QueryRow(ctx, checkoutGetByCardAuthorizationIDQuery, cardAuthorizationID))
	if err != nil {
		return nil, toAppErr(err, "failed to get checkout by card authorization", slog.String("card_authorization_id", cardAuthorizationID))
	}
	return c, nil
}

// SetCardAuthorization stores the card authorization of a Held checkout's
// order; it changes no other attribute.
func (r *CheckoutRepository) SetCardAuthorization(ctx context.Context, id entity.CheckoutID, cardAuthorizationID string) error {
	tag, err := r.db.Pool.Exec(ctx, checkoutSetCardAuthorizationQuery, string(id), cardAuthorizationID)
	if err != nil {
		return toAppErr(err, "failed to set checkout card authorization", slog.String("checkout_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	return apperr.New(codes.FailedPrecondition, "checkout is not Held or its order has another card authorization",
		slog.String("checkout_id", string(id)))
}

// Commit makes a holding checkout Committed under its stock's row lock.
func (r *CheckoutRepository) Commit(ctx context.Context, id entity.CheckoutID, now time.Time) (entity.CommitOutcome, error) {
	attrs := slog.String("checkout_id", string(id))
	current, err := r.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if current.Status == entity.CheckoutStatusCommitted || current.Status == entity.CheckoutStatusCompleted {
		return entity.CommitOutcomeCommitted, nil
	}
	if current.Status != entity.CheckoutStatusHeld {
		return entity.CommitOutcomeNotHeld, nil
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, toAppErr(err, "failed to begin checkout commit", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, ticketStockLockQuery, string(current.Order.TicketStockID)); err != nil {
		return 0, toAppErr(err, "failed to lock ticket stock", attrs)
	}
	tag, err := tx.Exec(ctx, checkoutCommitQuery, string(id), now)
	if err != nil {
		return 0, toAppErr(err, "failed to commit checkout", attrs)
	}
	if tag.RowsAffected() == 0 {
		// Not holding any more: lapsed, released, or committed by a
		// concurrent caller.
		_ = tx.Rollback(ctx)
		after, err := r.Get(ctx, id)
		if err != nil {
			return 0, err
		}
		if after.Status == entity.CheckoutStatusCommitted || after.Status == entity.CheckoutStatusCompleted {
			return entity.CommitOutcomeCommitted, nil
		}
		return entity.CommitOutcomeNotHeld, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, toAppErr(err, "failed to commit checkout commit", attrs)
	}
	return entity.CommitOutcomeCommitted, nil
}

// Release makes a lapsed Held checkout Expired and its order Void, and reports
// whether it changed it.
func (r *CheckoutRepository) Release(ctx context.Context, id entity.CheckoutID, now time.Time) (bool, error) {
	attrs := slog.String("checkout_id", string(id))
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return false, toAppErr(err, "failed to begin checkout release", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID string
	if err := tx.QueryRow(ctx, checkoutReleaseQuery, string(id), now).Scan(&orderID); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, toAppErr(err, "failed to release checkout", attrs)
		}
		_ = tx.Rollback(ctx)
		if _, err := r.Get(ctx, id); err != nil {
			return false, err
		}
		return false, nil
	}
	if _, err := tx.Exec(ctx, orderVoidQuery, orderID); err != nil {
		return false, toAppErr(err, "failed to void the released checkout's order", attrs)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, toAppErr(err, "failed to commit checkout release", attrs)
	}
	return true, nil
}

// RevertCommit makes a Committed, uncharged checkout Released and its order
// Void, under the stock's row lock.
func (r *CheckoutRepository) RevertCommit(ctx context.Context, id entity.CheckoutID) error {
	attrs := slog.String("checkout_id", string(id))
	current, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != entity.CheckoutStatusCommitted {
		return nil
	}
	if current.IsCharged() {
		return apperr.New(codes.FailedPrecondition, "a charged checkout cannot be reverted", attrs)
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin checkout revert", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, ticketStockLockQuery, string(current.Order.TicketStockID)); err != nil {
		return toAppErr(err, "failed to lock ticket stock", attrs)
	}
	var orderID string
	if err := tx.QueryRow(ctx, checkoutRevertCommitQuery, string(id)).Scan(&orderID); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return toAppErr(err, "failed to revert checkout commit", attrs)
		}
		// Changed meanwhile: charged, completed or reverted by another caller.
		_ = tx.Rollback(ctx)
		after, err := r.Get(ctx, id)
		if err != nil {
			return err
		}
		if after.IsCharged() {
			return apperr.New(codes.FailedPrecondition, "a charged checkout cannot be reverted", attrs)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, orderVoidQuery, orderID); err != nil {
		return toAppErr(err, "failed to void the reverted checkout's order", attrs)
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit checkout revert", attrs)
	}
	return nil
}

// RecordCapture stores the capture time of a Committed checkout once.
func (r *CheckoutRepository) RecordCapture(ctx context.Context, id entity.CheckoutID, at time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, checkoutRecordCaptureQuery, string(id), at)
	if err != nil {
		return toAppErr(err, "failed to record checkout capture", slog.String("checkout_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	current, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.IsCharged() {
		return nil
	}
	return apperr.New(codes.FailedPrecondition, "checkout is not Committed", slog.String("checkout_id", string(id)))
}

// RecordCardAuthorizationRelease sets the card authorization release time of
// an Expired or Released checkout whose order has a card authorization, once.
func (r *CheckoutRepository) RecordCardAuthorizationRelease(ctx context.Context, id entity.CheckoutID, at time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, checkoutRecordCardAuthorizationReleaseQuery, string(id), at)
	if err != nil {
		return toAppErr(err, "failed to record card authorization release", slog.String("checkout_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	current, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	ended := current.Status == entity.CheckoutStatusExpired || current.Status == entity.CheckoutStatusReleased
	if ended && current.CardAuthorizationReleaseTime != nil {
		return nil
	}
	return apperr.New(codes.FailedPrecondition, "checkout is not an ended checkout whose order has a card authorization",
		slog.String("checkout_id", string(id)))
}

// ListByUser returns every checkout of the user, newest first.
func (r *CheckoutRepository) ListByUser(ctx context.Context, userID entity.UserID) ([]*entity.Checkout, error) {
	return r.list(ctx, checkoutListByUserQuery, []any{string(userID)}, slog.String("user_id", string(userID)))
}

// ListDue returns the lapsed holds, the card authorizations to give back and
// the stalled commits, oldest hold first.
func (r *CheckoutRepository) ListDue(ctx context.Context, now time.Time) ([]*entity.Checkout, error) {
	return r.list(ctx, checkoutListDueQuery, []any{now, now.Add(-entity.CheckoutStalledCommitAge)})
}

// list runs a checkoutSelect query and collects the checkouts.
func (r *CheckoutRepository) list(ctx context.Context, query string, args []any, attrs ...slog.Attr) ([]*entity.Checkout, error) {
	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, toAppErr(err, "failed to list checkouts", attrs...)
	}
	checkouts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.Checkout, error) { return scanCheckout(row) })
	if err != nil {
		return nil, toAppErr(err, "failed to scan checkouts", attrs...)
	}
	return checkouts, nil
}

// Serialize runs fn while holding an advisory lock keyed on the checkout,
// taken in a transaction on its own connection. fn's own writes run on other
// connections, so they never wait for this lock.
func (r *CheckoutRepository) Serialize(ctx context.Context, id entity.CheckoutID, fn func(ctx context.Context) error) error {
	attrs := slog.String("checkout_id", string(id))
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin checkout lock", attrs)
	}
	// Rollback releases the lock; it is the only way the transaction ends.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, checkoutAdvisoryLockQuery, string(id)); err != nil {
		return toAppErr(err, "failed to lock checkout", attrs)
	}
	return fn(ctx)
}

// scanCheckout maps one row selected with checkoutSelect: the checkout's own
// columns, then its order's in orderSelect's order.
func scanCheckout(row pgx.Row) (*entity.Checkout, error) {
	var (
		c                                      entity.Checkout
		id, orderID, saleID                    string
		authReleasedAt, committedAt, captureAt sql.NullTime
		status                                 int16
		orderCols                              orderColumns
	)
	dest := append([]any{&id, &orderID, &saleID, &authReleasedAt, &status, &c.HoldExpireTime, &committedAt, &captureAt}, orderCols.dest()...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	c.ID = entity.CheckoutID(id)
	c.OrderID = entity.OrderID(orderID)
	c.TicketSaleID = entity.TicketSaleID(saleID)
	c.Status = entity.CheckoutStatus(status)
	c.CardAuthorizationReleaseTime = optionalTime(authReleasedAt)
	c.CommitTime = optionalTime(committedAt)
	c.CaptureTime = optionalTime(captureAt)
	c.Order = orderCols.order()
	return &c, nil
}

// optionalTime returns a pointer to the time of a valid sql.NullTime, or nil.
func optionalTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
