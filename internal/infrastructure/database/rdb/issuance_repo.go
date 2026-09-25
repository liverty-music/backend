package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// IssuanceRepository implements entity.IssuanceRepository for PostgreSQL: it
// persists an Order, its N tickets, and its Held settlement atomically in one
// transaction (backend#468).
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
	// issuanceInsertSettlementQuery inserts the Held settlement row for the
	// issued Order. Settlement.organizer_id/event_id are NOT NULL, so the
	// caller (IssuanceUseCase) must resolve the Organizer before calling Issue.
	issuanceInsertSettlementQuery = `
		INSERT INTO settlements (id, order_id, organizer_id, event_id, status, settled_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	// issuanceInsertSettlementSplitQuery inserts one payout split for the
	// settlement just created. transfer_ref/transfer_reversal_ref default to
	// NULL until the payout sweeper releases the settlement.
	issuanceInsertSettlementSplitQuery = `
		INSERT INTO settlement_splits (settlement_id, payee_organizer_id, amount)
		VALUES ($1, $2, $3)
	`
	// issuanceListAwaitingQuery returns Won (state 2) applications that have no
	// order yet — the issuance sweeper work-list.
	issuanceListAwaitingQuery = `
		SELECT ta.id
		FROM ticket_applications ta
		LEFT JOIN orders o ON o.application_id = ta.id
		WHERE ta.state = 2 AND o.id IS NULL
		ORDER BY ta.id
	`
)

// NewIssuanceRepository creates a new issuance repository instance.
func NewIssuanceRepository(db *Database) *IssuanceRepository {
	return &IssuanceRepository{db: db}
}

// Issue atomically inserts the order, its N tickets, and its Held settlement
// (with splits) in one transaction. A duplicate order for the same application
// (unique index on orders.application_id) surfaces as apperr.ErrAlreadyExists
// so the caller re-reads the existing order instead of double-issuing.
//
// settlement is required (non-nil); the caller resolves the event's Organizer
// before calling Issue so the Order is never committed without its payout record.
func (r *IssuanceRepository) Issue(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error {
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

	if _, err := tx.Exec(ctx, issuanceInsertSettlementQuery,
		string(settlement.ID), string(settlement.OrderID), settlement.OrganizerID, settlement.EventID,
		int16(settlement.Status), settlement.CreatedTime,
	); err != nil {
		return toAppErr(err, "failed to insert settlement", slog.String("settlement_id", string(settlement.ID)))
	}
	for _, split := range settlement.Splits {
		if _, err := tx.Exec(ctx, issuanceInsertSettlementSplitQuery,
			string(settlement.ID), split.PayeeOrganizerID, split.Amount,
		); err != nil {
			return toAppErr(err, "failed to insert settlement split",
				slog.String("settlement_id", string(settlement.ID)),
				slog.String("payee_organizer_id", split.PayeeOrganizerID))
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

// ListApplicationIDsAwaitingIssuance returns Won applications without an order.
func (r *IssuanceRepository) ListApplicationIDsAwaitingIssuance(ctx context.Context) ([]entity.TicketApplicationID, error) {
	rows, err := r.db.Pool.Query(ctx, issuanceListAwaitingQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list applications awaiting issuance")
	}
	defer rows.Close()

	var ids []entity.TicketApplicationID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, toAppErr(err, "failed to scan application id awaiting issuance")
		}
		ids = append(ids, entity.TicketApplicationID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating applications awaiting issuance")
	}
	return ids, nil
}

// nullableUUID returns nil for an empty id so a NULL is written to a nullable
// UUID column, or the id string otherwise.
func nullableUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
