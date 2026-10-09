package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// OrderRepository implements entity.OrderRepository for PostgreSQL.
type OrderRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.OrderRepository = (*OrderRepository)(nil)

const (
	orderSelectColumns = `
		id, buyer_id, application_id, provider, payment_intent_ref, payment_method_ref,
		card_brand, card_last4, status, amount, currency, paid_at, refund_ref,
		reservation_id, confirmation_sent_at
	`
	orderGetQuery                   = `SELECT ` + orderSelectColumns + ` FROM orders WHERE id = $1`
	orderGetByApplicationIDQuery    = `SELECT ` + orderSelectColumns + ` FROM orders WHERE application_id = $1`
	orderGetByReservationIDQuery    = `SELECT ` + orderSelectColumns + ` FROM orders WHERE reservation_id = $1`
	orderGetByPaymentIntentRefQuery = `SELECT ` + orderSelectColumns + ` FROM orders WHERE payment_intent_ref = $1`
	// orderMarkConfirmationSentQuery sets the confirmation-sent time only when
	// it is unset, so a second call keeps the first time.
	orderMarkConfirmationSentQuery = `
		UPDATE orders SET confirmation_sent_at = $2
		WHERE id = $1 AND confirmation_sent_at IS NULL
	`
	orderExistsQuery       = `SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`
	orderUpdateStatusQuery = `UPDATE orders SET status = $2 WHERE id = $1`
	orderListByBuyerQuery  = `SELECT ` + orderSelectColumns + ` FROM orders WHERE buyer_id = $1 ORDER BY paid_at, id`
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

// GetByApplicationID retrieves the order created for the given winning
// application. Returns apperr.ErrNotFound when issuance has not run.
func (r *OrderRepository) GetByApplicationID(ctx context.Context, applicationID entity.TicketApplicationID) (*entity.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderGetByApplicationIDQuery, string(applicationID))
	order, err := scanOrder(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get order by application", slog.String("application_id", string(applicationID)))
	}
	return order, nil
}

// GetByReservationID retrieves the order whose source is the given
// reservation. Returns apperr.ErrNotFound when the reservation has no order.
func (r *OrderRepository) GetByReservationID(ctx context.Context, reservationID entity.ReservationID) (*entity.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderGetByReservationIDQuery, string(reservationID))
	order, err := scanOrder(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get order by reservation", slog.String("reservation_id", string(reservationID)))
	}
	return order, nil
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

// GetByPaymentIntentRef retrieves the order whose Payment.PaymentIntentRef
// matches the given pi_ value. Used by the webhook service to resolve a
// dispute's payment_intent → Order without exposing repo access to the HTTP
// handler layer.
func (r *OrderRepository) GetByPaymentIntentRef(ctx context.Context, paymentIntentRef string) (*entity.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderGetByPaymentIntentRefQuery, paymentIntentRef)
	order, err := scanOrder(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get order by payment intent ref",
			slog.String("payment_intent_ref", paymentIntentRef))
	}
	return order, nil
}

// UpdateStatus changes the status of the order identified by id.
func (r *OrderRepository) UpdateStatus(ctx context.Context, id entity.OrderID, status entity.OrderStatus) error {
	tag, err := r.db.Pool.Exec(ctx, orderUpdateStatusQuery, string(id), int16(status))
	if err != nil {
		return toAppErr(err, "failed to update order status", slog.String("order_id", string(id)))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "order not found")
	}
	return nil
}

// ListByBuyer returns every order the given user bought, in any status, oldest
// first. Returns an empty slice when the user bought nothing.
func (r *OrderRepository) ListByBuyer(ctx context.Context, buyerID entity.UserID) ([]*entity.Order, error) {
	rows, err := r.db.Pool.Query(ctx, orderListByBuyerQuery, string(buyerID))
	if err != nil {
		return nil, toAppErr(err, "failed to list orders by buyer", slog.String("buyer_id", string(buyerID)))
	}
	defer rows.Close()

	orders := []*entity.Order{}
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan order", slog.String("buyer_id", string(buyerID)))
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate orders", slog.String("buyer_id", string(buyerID)))
	}
	return orders, nil
}

// orderScanner is satisfied by both pgx.Row and pgx.Rows, so scanOrder works
// for single-row and multi-row reads.
type orderScanner interface {
	Scan(dest ...any) error
}

// scanOrder maps one orders row into an entity.Order.
func scanOrder(s orderScanner) (*entity.Order, error) {
	var (
		o                  entity.Order
		id                 string
		buyerID            string
		appID              sql.NullString
		provider           int16
		status             int16
		reservationID      sql.NullString
		confirmationSentAt sql.NullTime
	)
	if err := s.Scan(
		&id, &buyerID, &appID, &provider,
		&o.Payment.PaymentIntentRef, &o.Payment.PaymentMethodRef,
		&o.Payment.CardBrand, &o.Payment.CardLast4,
		&status, &o.Amount, &o.Currency, &o.PaidTime,
		&o.RefundRef, &reservationID, &confirmationSentAt,
	); err != nil {
		return nil, err
	}
	o.ID = entity.OrderID(id)
	o.BuyerID = entity.UserID(buyerID)
	if appID.Valid {
		o.ApplicationID = entity.TicketApplicationID(appID.String)
	}
	if reservationID.Valid {
		o.ReservationID = entity.ReservationID(reservationID.String)
	}
	if confirmationSentAt.Valid {
		sentAt := confirmationSentAt.Time
		o.ConfirmationSentTime = &sentAt
	}
	o.Payment.Provider = entity.PaymentProvider(provider)
	o.Status = entity.OrderStatus(status)
	return &o, nil
}
