package rdb

import (
	"context"
	"log/slog"

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
		card_brand, card_last4, status, amount, currency, paid_at, refund_ref
	`
	orderGetQuery                   = `SELECT ` + orderSelectColumns + ` FROM orders WHERE id = $1`
	orderGetByApplicationIDQuery    = `SELECT ` + orderSelectColumns + ` FROM orders WHERE application_id = $1`
	orderGetByPaymentIntentRefQuery = `SELECT ` + orderSelectColumns + ` FROM orders WHERE payment_intent_ref = $1`
	orderUpdateStatusQuery          = `UPDATE orders SET status = $2 WHERE id = $1`
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

// orderScanner is satisfied by both pgx.Row and pgx.Rows, so scanOrder works
// for single-row and multi-row reads.
type orderScanner interface {
	Scan(dest ...any) error
}

// scanOrder maps one orders row into an entity.Order.
func scanOrder(s orderScanner) (*entity.Order, error) {
	var (
		o        entity.Order
		id       string
		buyerID  string
		appID    string
		provider int16
		status   int16
	)
	if err := s.Scan(
		&id, &buyerID, &appID, &provider,
		&o.Payment.PaymentIntentRef, &o.Payment.PaymentMethodRef,
		&o.Payment.CardBrand, &o.Payment.CardLast4,
		&status, &o.Amount, &o.Currency, &o.PaidTime,
		&o.RefundRef,
	); err != nil {
		return nil, err
	}
	o.ID = entity.OrderID(id)
	o.BuyerID = entity.UserID(buyerID)
	o.ApplicationID = entity.TicketApplicationID(appID)
	o.Payment.Provider = entity.PaymentProvider(provider)
	o.Status = entity.OrderStatus(status)
	return &o, nil
}
