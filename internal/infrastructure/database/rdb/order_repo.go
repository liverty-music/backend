package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// OrderRepository is the PostgreSQL part of entity.OrderRepository; package
// repository adds the payment provider part.
type OrderRepository struct {
	db *Database
}

// orderSelect reads orders (aliased o) with the id of the LotteryEntry or
// Checkout that names each.
const orderSelect = `
	SELECT o.id, o.user_id, o.ticket_stock_id, o.ticket_count, o.referral_code, o.card_authorization_id,
		o.provider, o.provider_payment_id, o.provider_payment_method_id, o.card_brand, o.card_last4,
		o.status, o.amount, o.currency, o.pay_at, o.provider_refund_id, o.confirmation_send_at,
		le.id, c.id
	FROM orders o
	LEFT JOIN lottery_entries le ON le.order_id = o.id
	LEFT JOIN checkouts c ON c.order_id = o.id
`

const (
	orderGetQuery                 = orderSelect + ` WHERE o.id = $1`
	orderGetByLotteryEntryIDQuery = `
		SELECT o.id FROM lottery_entries le JOIN orders o ON o.id = le.order_id WHERE le.id = $1
	`
	orderGetByCheckoutIDQuery = `
		SELECT o.id FROM checkouts c JOIN orders o ON o.id = c.order_id WHERE c.id = $1
	`
	orderGetByProviderPaymentIDQuery = orderSelect + ` WHERE o.provider_payment_id = $1`
	// orderMarkConfirmationSentQuery sets the confirmation-sent time only when
	// it is unset, so a second call keeps the first time.
	orderMarkConfirmationSentQuery = `
		UPDATE orders SET confirmation_send_at = $2
		WHERE id = $1 AND confirmation_send_at IS NULL
	`
	orderExistsQuery = `SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`
	// orderListByUserQuery lists the user's Paid and Refunded orders.
	orderListByUserQuery = orderSelect + ` WHERE o.user_id = $1 AND o.status IN (1, 2) ORDER BY o.pay_at, o.id`
	// orderListByEventQuery takes the event as a one-element uuid array, the
	// shape eventOrderIDsSubquery expects, and lists its Paid and Refunded
	// orders.
	orderListByEventQuery = orderSelect + ` WHERE o.id IN (` + eventOrderIDsSubquery + `) AND o.status IN (1, 2) ORDER BY o.pay_at, o.id`
	orderGetEventIDQuery  = `
		SELECT s.event_id FROM orders o JOIN ticket_stocks s ON s.id = o.ticket_stock_id WHERE o.id = $1
	`
	// orderListReferralStatsByEventQuery counts the event's Paid orders
	// (status 1) per referral code; a NULL code is the direct group. COLLATE
	// "C" compares codes character by character.
	orderListReferralStatsByEventQuery = `
		SELECT o.referral_code, count(*)::int, sum(o.ticket_count)::int, sum(o.amount)::bigint
		FROM orders o
		JOIN ticket_stocks s ON s.id = o.ticket_stock_id
		WHERE s.event_id = $1 AND o.status = 1
		GROUP BY 1
		ORDER BY count(*) DESC, o.referral_code COLLATE "C"
	`
	// orderHeldForEventQuery counts what the user ($1) holds for the event
	// ($2), leaving out the order $3 when given, at $4: the Issued tickets of
	// the user's Paid orders, plus the ticket counts of the user's Pending
	// orders that still hold or may receive tickets (a checkout holding at $4
	// or Committed, or a lottery entry Entered or Won), with the seat type of
	// the orders counted. A Paid order whose tickets are all Voided counts 0
	// and names no seat type.
	orderHeldForEventQuery = `
		WITH counted AS (
			SELECT s.seat_type_id,
				CASE WHEN o.status = 1
					THEN (SELECT count(*) FROM tickets t WHERE t.order_id = o.id AND t.status = 1)
					ELSE o.ticket_count
				END AS n
			FROM orders o
			JOIN ticket_stocks s ON s.id = o.ticket_stock_id
			LEFT JOIN checkouts c ON c.order_id = o.id
			LEFT JOIN lottery_entries le ON le.order_id = o.id
			WHERE o.user_id = $1 AND s.event_id = $2 AND ($3::uuid IS NULL OR o.id <> $3::uuid)
			  AND (o.status = 1
			       OR (o.status = 4 AND ((c.status = 1 AND c.hold_expire_at > $4) OR c.status = 2 OR le.state IN (1, 2))))
		)
		SELECT COALESCE(SUM(n), 0)::int, (array_agg(seat_type_id) FILTER (WHERE n > 0))[1] FROM counted
	`
)

// NewOrderRepository creates a new order repository instance.
func NewOrderRepository(db *Database) *OrderRepository {
	return &OrderRepository{db: db}
}

// Get retrieves a single order by its ID. Returns apperr.ErrNotFound when absent.
func (r *OrderRepository) Get(ctx context.Context, id entity.OrderID) (*entity.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderGetQuery, string(id))
	order, err := scanOrder(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get order", slog.String("order_id", string(id)))
	}
	return order, nil
}

// GetByLotteryEntryID returns the order the given entry names, in any
// status. Returns apperr.ErrNotFound when no entry has the id.
func (r *OrderRepository) GetByLotteryEntryID(ctx context.Context, lotteryEntryID entity.LotteryEntryID) (*entity.Order, error) {
	return r.getVia(ctx, orderGetByLotteryEntryIDQuery, string(lotteryEntryID), slog.String("lottery_entry_id", string(lotteryEntryID)))
}

// GetByCheckoutID returns the order the given checkout names, in any status.
// Returns apperr.ErrNotFound when no checkout has the id.
func (r *OrderRepository) GetByCheckoutID(ctx context.Context, checkoutID entity.CheckoutID) (*entity.Order, error) {
	return r.getVia(ctx, orderGetByCheckoutIDQuery, string(checkoutID), slog.String("checkout_id", string(checkoutID)))
}

// getVia resolves the id of an order with query and reads the order.
func (r *OrderRepository) getVia(ctx context.Context, query, key string, attr slog.Attr) (*entity.Order, error) {
	var id string
	if err := r.db.Pool.QueryRow(ctx, query, key).Scan(&id); err != nil {
		return nil, toAppErr(err, "failed to resolve order", attr)
	}
	return r.Get(ctx, entity.OrderID(id))
}

// GetHeldForEvent returns what the user holds for the event at at, leaving
// out the order except names.
func (r *OrderRepository) GetHeldForEvent(ctx context.Context, userID entity.UserID, eventID string, except *entity.OrderID, at time.Time) (entity.HeldTickets, error) {
	held, err := getHeldForEvent(ctx, r.db.Pool, userID, eventID, except, at)
	if err != nil {
		return entity.HeldTickets{}, toAppErr(err, "failed to read the tickets held for the event",
			slog.String("user_id", string(userID)), slog.String("event_id", eventID))
	}
	return held, nil
}

// rowScanner runs a query returning one row: a pool or a transaction.
type rowScanner interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// getHeldForEvent runs orderHeldForEventQuery on q, so the purchase paths can
// read it in their transaction under the user's row lock.
func getHeldForEvent(ctx context.Context, q rowScanner, userID entity.UserID, eventID string, except *entity.OrderID, at time.Time) (entity.HeldTickets, error) {
	var (
		held       entity.HeldTickets
		seatTypeID sql.NullString
		exceptID   any
	)
	if except != nil {
		exceptID = string(*except)
	}
	if err := q.QueryRow(ctx, orderHeldForEventQuery, string(userID), eventID, exceptID, at).Scan(&held.Count, &seatTypeID); err != nil {
		return entity.HeldTickets{}, err
	}
	if seatTypeID.Valid {
		id := entity.SeatTypeID(seatTypeID.String)
		held.SeatTypeID = &id
	}
	return held, nil
}

// MarkConfirmationSent sets the order's confirmation-sent time when it is
// unset; an already set time is kept. Returns apperr.ErrNotFound when no
// order has the id.
func (r *OrderRepository) MarkConfirmationSent(ctx context.Context, id entity.OrderID, at time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, orderMarkConfirmationSentQuery, string(id), at)
	if err != nil {
		return toAppErr(err, "failed to mark order confirmation sent", slog.String("order_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := r.db.Pool.QueryRow(ctx, orderExistsQuery, string(id)).Scan(&exists); err != nil {
		return toAppErr(err, "failed to check order", slog.String("order_id", string(id)))
	}
	if !exists {
		return apperr.New(codes.NotFound, "order not found")
	}
	return nil
}

// GetByProviderPaymentID retrieves the order whose Payment.ProviderPaymentID
// matches the given pi_ value. Used by the webhook service to resolve a
// dispute's provider payment ID → Order without exposing repo access to the HTTP
// handler layer.
func (r *OrderRepository) GetByProviderPaymentID(ctx context.Context, providerPaymentID string) (*entity.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderGetByProviderPaymentIDQuery, providerPaymentID)
	order, err := scanOrder(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get order by provider payment ID",
			slog.String("provider_payment_id", providerPaymentID))
	}
	return order, nil
}

// ListByUser returns the Paid and Refunded orders of the given user, oldest
// first. Returns an empty slice when the user bought nothing.
func (r *OrderRepository) ListByUser(ctx context.Context, userID entity.UserID) ([]*entity.Order, error) {
	rows, err := r.db.Pool.Query(ctx, orderListByUserQuery, string(userID))
	if err != nil {
		return nil, toAppErr(err, "failed to list orders by user", slog.String("user_id", string(userID)))
	}
	defer rows.Close()

	orders := []*entity.Order{}
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan order", slog.String("user_id", string(userID)))
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate orders", slog.String("user_id", string(userID)))
	}
	return orders, nil
}

// ListByEvent returns the Paid and Refunded orders of the event's ticket
// stocks, oldest pay time first. Returns an empty slice when the event has no
// such order or does not exist.
func (r *OrderRepository) ListByEvent(ctx context.Context, eventID string) ([]*entity.Order, error) {
	if eventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event id must not be empty")
	}
	rows, err := r.db.Pool.Query(ctx, orderListByEventQuery, []string{eventID})
	if err != nil {
		return nil, toAppErr(err, "failed to list orders by event", slog.String("event_id", eventID))
	}
	defer rows.Close()

	orders := []*entity.Order{}
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan order", slog.String("event_id", eventID))
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate orders", slog.String("event_id", eventID))
	}
	return orders, nil
}

// GetEventID returns the id of the event of the order's ticket stock. Returns
// apperr.ErrNotFound when no order has the id.
func (r *OrderRepository) GetEventID(ctx context.Context, id entity.OrderID) (string, error) {
	var eventID string
	if err := r.db.Pool.QueryRow(ctx, orderGetEventIDQuery, string(id)).Scan(&eventID); err != nil {
		return "", toAppErr(err, "failed to get order event", slog.String("order_id", string(id)))
	}
	return eventID, nil
}

// orderScanner is satisfied by both pgx.Row and pgx.Rows, so scanOrder works
// for single-row and multi-row reads.
type orderScanner interface {
	Scan(dest ...any) error
}

// scanOrder maps one row selected with orderSelect into an entity.Order.
func scanOrder(s orderScanner) (*entity.Order, error) {
	var cols orderColumns
	if err := s.Scan(cols.dest()...); err != nil {
		return nil, err
	}
	return cols.order(), nil
}

// orderColumns holds the scan targets of the order columns of orderSelect,
// which checkoutSelect and the lottery entry reads repeat.
type orderColumns struct {
	o                                                    entity.Order
	id, userID, stockID                                  string
	referralCode, cardAuthorizationID, providerPaymentID sql.NullString
	lotteryEntryID, checkoutID                           sql.NullString
	provider, status                                     int16
	payAt, confirmationSentAt                            sql.NullTime
}

// dest returns the scan targets in column order.
func (c *orderColumns) dest() []any {
	return []any{
		&c.id, &c.userID, &c.stockID, &c.o.TicketCount, &c.referralCode, &c.cardAuthorizationID,
		&c.provider, &c.providerPaymentID, &c.o.Payment.ProviderPaymentMethodID, &c.o.Payment.CardBrand, &c.o.Payment.CardLast4,
		&c.status, &c.o.Amount, &c.o.Currency, &c.payAt, &c.o.ProviderRefundID, &c.confirmationSentAt,
		&c.lotteryEntryID, &c.checkoutID,
	}
}

// order returns the scanned order.
func (c *orderColumns) order() *entity.Order {
	o := c.o
	o.ID = entity.OrderID(c.id)
	o.UserID = entity.UserID(c.userID)
	o.TicketStockID = entity.TicketStockID(c.stockID)
	if c.referralCode.Valid {
		code := entity.ReferralCode(c.referralCode.String)
		o.ReferralCode = &code
	}
	if c.cardAuthorizationID.Valid {
		o.CardAuthorizationID = c.cardAuthorizationID.String
	}
	if c.providerPaymentID.Valid {
		o.Payment.ProviderPaymentID = c.providerPaymentID.String
	}
	if c.lotteryEntryID.Valid {
		o.LotteryEntryID = entity.LotteryEntryID(c.lotteryEntryID.String)
	}
	if c.checkoutID.Valid {
		o.CheckoutID = entity.CheckoutID(c.checkoutID.String)
	}
	o.PayTime = optionalTime(c.payAt)
	o.ConfirmationSendTime = optionalTime(c.confirmationSentAt)
	o.Payment.Provider = entity.PaymentProvider(c.provider)
	o.Status = entity.OrderStatus(c.status)
	return &o
}

// ListReferralStatsByEvent counts the event's Paid Orders per referral code.
func (r *OrderRepository) ListReferralStatsByEvent(ctx context.Context, eventID string) ([]entity.ReferralCodeStats, error) {
	attrs := slog.String("event_id", eventID)
	rows, err := r.db.Pool.Query(ctx, orderListReferralStatsByEventQuery, eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list referral stats", attrs)
	}
	defer rows.Close()

	stats := []entity.ReferralCodeStats{}
	for rows.Next() {
		var (
			code sql.NullString
			s    entity.ReferralCodeStats
		)
		if err := rows.Scan(&code, &s.OrderCount, &s.TicketCount, &s.Amount); err != nil {
			return nil, toAppErr(err, "failed to scan referral stats", attrs)
		}
		if code.Valid {
			c := entity.ReferralCode(code.String)
			s.Code = &c
		}
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate referral stats", attrs)
	}
	return stats, nil
}
