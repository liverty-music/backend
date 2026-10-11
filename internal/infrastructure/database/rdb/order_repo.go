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
		id, user_id, lottery_entry_id, provider, provider_payment_id, provider_payment_method_id,
		card_brand, card_last4, status, amount, currency, pay_at, provider_refund_id,
		checkout_id, confirmation_send_at
	`
	orderGetQuery                    = `SELECT ` + orderSelectColumns + ` FROM orders WHERE id = $1`
	orderGetByLotteryEntryIDQuery    = `SELECT ` + orderSelectColumns + ` FROM orders WHERE lottery_entry_id = $1`
	orderGetByCheckoutIDQuery        = `SELECT ` + orderSelectColumns + ` FROM orders WHERE checkout_id = $1`
	orderGetByProviderPaymentIDQuery = `SELECT ` + orderSelectColumns + ` FROM orders WHERE provider_payment_id = $1`
	// orderMarkConfirmationSentQuery sets the confirmation-sent time only when
	// it is unset, so a second call keeps the first time.
	orderMarkConfirmationSentQuery = `
		UPDATE orders SET confirmation_send_at = $2
		WHERE id = $1 AND confirmation_send_at IS NULL
	`
	orderExistsQuery       = `SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`
	orderUpdateStatusQuery = `UPDATE orders SET status = $2 WHERE id = $1`
	orderListByUserQuery   = `SELECT ` + orderSelectColumns + ` FROM orders WHERE user_id = $1 ORDER BY pay_at, id`
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

// GetByLotteryEntryID retrieves the order created for the given won lottery
// entry. Returns apperr.ErrNotFound when issuance has not run.
func (r *OrderRepository) GetByLotteryEntryID(ctx context.Context, lotteryEntryID entity.LotteryEntryID) (*entity.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderGetByLotteryEntryIDQuery, string(lotteryEntryID))
	order, err := scanOrder(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get order by lottery entry", slog.String("lottery_entry_id", string(lotteryEntryID)))
	}
	return order, nil
}

// GetByCheckoutID retrieves the order whose source is the given checkout.
// Returns apperr.ErrNotFound when the checkout has no order.
func (r *OrderRepository) GetByCheckoutID(ctx context.Context, checkoutID entity.CheckoutID) (*entity.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderGetByCheckoutIDQuery, string(checkoutID))
	order, err := scanOrder(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get order by checkout", slog.String("checkout_id", string(checkoutID)))
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

// GetByProviderPaymentID retrieves the order whose Payment.ProviderPaymentID
// matches the given pi_ value. Used by the webhook service to resolve a
// dispute's payment_intent → Order without exposing repo access to the HTTP
// handler layer.
func (r *OrderRepository) GetByProviderPaymentID(ctx context.Context, providerPaymentID string) (*entity.Order, error) {
	row := r.db.Pool.QueryRow(ctx, orderGetByProviderPaymentIDQuery, providerPaymentID)
	order, err := scanOrder(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get order by payment intent ref",
			slog.String("provider_payment_id", providerPaymentID))
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

// ListByUser returns every order the given user bought, in any status, oldest
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
		userID             string
		lotteryEntryID     sql.NullString
		provider           int16
		status             int16
		checkoutID         sql.NullString
		confirmationSentAt sql.NullTime
	)
	if err := s.Scan(
		&id, &userID, &lotteryEntryID, &provider,
		&o.Payment.ProviderPaymentID, &o.Payment.ProviderPaymentMethodID,
		&o.Payment.CardBrand, &o.Payment.CardLast4,
		&status, &o.Amount, &o.Currency, &o.PayTime,
		&o.ProviderRefundID, &checkoutID, &confirmationSentAt,
	); err != nil {
		return nil, err
	}
	o.ID = entity.OrderID(id)
	o.UserID = entity.UserID(userID)
	if lotteryEntryID.Valid {
		o.LotteryEntryID = entity.LotteryEntryID(lotteryEntryID.String)
	}
	if checkoutID.Valid {
		o.CheckoutID = entity.CheckoutID(checkoutID.String)
	}
	if confirmationSentAt.Valid {
		sentAt := confirmationSentAt.Time
		o.ConfirmationSendTime = &sentAt
	}
	o.Payment.Provider = entity.PaymentProvider(provider)
	o.Status = entity.OrderStatus(status)
	return &o, nil
}
