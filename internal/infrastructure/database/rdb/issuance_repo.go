package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// IssuanceRepository implements entity.IssuanceRepository for PostgreSQL: it
// persists an Order and its N tickets atomically in one transaction.
type IssuanceRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.IssuanceRepository = (*IssuanceRepository)(nil)

const (
	issuanceInsertOrderQuery = `
		INSERT INTO orders (
			id, buyer_id, application_id, provider, payment_intent_ref, payment_method_ref,
			card_brand, card_last4, status, amount, currency, paid_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	issuanceInsertTicketQuery = `
		INSERT INTO tickets (
			id, order_id, holder_id, event_id, holder_full_name, holder_phone_number,
			verified_identity_id, resale_without_consent_prohibited, status, issued_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
)

// NewIssuanceRepository creates a new issuance repository instance.
func NewIssuanceRepository(db *Database) *IssuanceRepository {
	return &IssuanceRepository{db: db}
}

// Issue atomically inserts the order and its N tickets in one transaction. A
// duplicate order for the same application (unique index on orders.application_id)
// surfaces as apperr.ErrAlreadyExists so the caller re-reads the existing order
// instead of double-issuing.
func (r *IssuanceRepository) Issue(ctx context.Context, order *entity.Order, tickets []*entity.Ticket) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin issuance transaction")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, issuanceInsertOrderQuery,
		string(order.ID), string(order.BuyerID), string(order.ApplicationID),
		int16(order.Payment.Provider), order.Payment.PaymentIntentRef, order.Payment.PaymentMethodRef,
		order.Payment.CardBrand, order.Payment.CardLast4,
		int16(order.Status), order.Amount, order.Currency, order.PaidTime,
	); err != nil {
		return toAppErr(err, "failed to insert order", slog.String("application_id", string(order.ApplicationID)))
	}

	for _, t := range tickets {
		if _, err := tx.Exec(ctx, issuanceInsertTicketQuery,
			string(t.ID), string(t.OrderID), string(t.HolderID), t.EventID,
			t.HolderIdentity.FullName, t.HolderIdentity.PhoneNumber,
			nullableUUID(t.VerifiedIdentityID), t.ResaleWithoutConsentProhibited,
			int16(t.Status), t.IssuedTime,
		); err != nil {
			return toAppErr(err, "failed to insert ticket", slog.String("ticket_id", string(t.ID)))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit issuance transaction")
	}

	r.db.logger.Info(ctx, "issued order and tickets",
		slog.String("entityType", "orders"),
		slog.String("orderID", string(order.ID)),
		slog.String("applicationID", string(order.ApplicationID)),
		slog.Int("ticketCount", len(tickets)),
	)
	return nil
}

// nullableUUID returns nil for an empty id so a NULL is written to a nullable
// UUID column, or the id string otherwise.
func nullableUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
