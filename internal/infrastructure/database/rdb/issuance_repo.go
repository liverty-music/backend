package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// IssuanceRepository implements entity.IssuanceRepository for PostgreSQL: it
// persists an Order, its N tickets and its Held settlement atomically in one
// transaction, and completes a source checkout (backend#468).
type IssuanceRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.IssuanceRepository = (*IssuanceRepository)(nil)

const (
	issuanceInsertOrderQuery = `
		INSERT INTO orders (
			id, user_id, lottery_entry_id, checkout_id, provider, provider_payment_id, provider_payment_method_id,
			card_brand, card_last4, status, amount, currency, pay_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	issuanceInsertTicketQuery = `
		INSERT INTO tickets (
			id, order_id, user_id, event_id, identity_verification_id,
			resale_without_consent_prohibited, status, issue_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	// issuanceInsertSettlementQuery inserts the Held settlement row for the
	// issued Order. Settlement.organizer_id/event_id are NOT NULL, so the
	// caller (IssuanceUseCase) must resolve the Organizer before calling Issue.
	issuanceInsertSettlementQuery = `
		INSERT INTO settlements (id, order_id, organizer_id, event_id, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	// issuanceInsertSettlementSplitQuery inserts one payout split for the
	// settlement just created. transfer_ref/transfer_reversal_ref default to
	// NULL until the payout sweeper releases the settlement.
	issuanceInsertSettlementSplitQuery = `
		INSERT INTO settlement_splits (settlement_id, payee_organizer_id, amount)
		VALUES ($1, $2, $3)
	`
	// issuanceCompleteCheckoutQuery completes the source checkout, only when
	// it is Committed and charged.
	issuanceCompleteCheckoutQuery = `
		UPDATE checkouts SET status = 3
		WHERE id = $1 AND status = 2 AND capture_at IS NOT NULL
	`
	issuanceCheckoutHasOrderQuery = `SELECT EXISTS (SELECT 1 FROM orders WHERE checkout_id = $1)`
	// issuanceListAwaitingQuery returns Won (state 2) lottery entries that have
	// no order yet — the issuance sweeper work-list.
	issuanceListAwaitingQuery = `
		SELECT le.id
		FROM lottery_entries le
		LEFT JOIN orders o ON o.lottery_entry_id = le.id
		WHERE le.state = 2 AND o.id IS NULL
		ORDER BY le.id
	`
)

// NewIssuanceRepository creates a new issuance repository instance.
func NewIssuanceRepository(db *Database) *IssuanceRepository {
	return &IssuanceRepository{db: db}
}

// Issue atomically inserts the order, its N tickets, and its Held settlement
// (with splits) in one transaction. A duplicate order for the same source
// (unique indexes on orders.lottery_entry_id and orders.checkout_id) surfaces as apperr.ErrAlreadyExists
// so the caller re-reads the existing order instead of double-issuing.
//
// settlement is required (non-nil); the caller resolves the event's Organizer
// before calling Issue so the Order is never committed without its payout record.
func (r *IssuanceRepository) Issue(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error {
	if err := order.ValidateSource(); err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, "invalid order source")
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin issuance transaction")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	// A checkout is completed first, so an uncharged one stores nothing.
	if order.CheckoutID != "" {
		tag, err := tx.Exec(ctx, issuanceCompleteCheckoutQuery, string(order.CheckoutID))
		if err != nil {
			return toAppErr(err, "failed to complete checkout", slog.String("checkout_id", string(order.CheckoutID)))
		}
		if tag.RowsAffected() == 0 {
			// A checkout already issued is Completed: report the duplicate.
			var issued bool
			if err := tx.QueryRow(ctx, issuanceCheckoutHasOrderQuery, string(order.CheckoutID)).Scan(&issued); err != nil {
				return toAppErr(err, "failed to check the checkout's order", slog.String("checkout_id", string(order.CheckoutID)))
			}
			if issued {
				return apperr.New(codes.AlreadyExists, "an order already exists for the checkout",
					slog.String("checkout_id", string(order.CheckoutID)))
			}
			return apperr.New(codes.FailedPrecondition, "checkout is not Committed and charged",
				slog.String("checkout_id", string(order.CheckoutID)))
		}
	}

	if _, err := tx.Exec(ctx, issuanceInsertOrderQuery,
		string(order.ID), string(order.UserID), nullableUUID(string(order.LotteryEntryID)),
		nullableUUID(string(order.CheckoutID)), int16(order.Payment.Provider), order.Payment.PaymentIntentRef, order.Payment.PaymentMethodRef,
		order.Payment.CardBrand, order.Payment.CardLast4,
		int16(order.Status), order.Amount, order.Currency, order.PaidTime,
	); err != nil {
		return toAppErr(err, "failed to insert order",
			slog.String("lottery_entry_id", string(order.LotteryEntryID)),
			slog.String("checkout_id", string(order.CheckoutID)))
	}

	for _, t := range tickets {
		if _, err := tx.Exec(ctx, issuanceInsertTicketQuery,
			string(t.ID), string(t.OrderID), string(t.UserID), t.EventID,
			nullableUUID(t.KycID), t.ResaleWithoutConsentProhibited,
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
		slog.String("lotteryEntryID", string(order.LotteryEntryID)),
		slog.String("checkoutID", string(order.CheckoutID)),
		slog.Int("ticketCount", len(tickets)),
	)
	return nil
}

// ListLotteryEntryIDsAwaitingIssuance returns Won lottery entries without an
// order.
func (r *IssuanceRepository) ListLotteryEntryIDsAwaitingIssuance(ctx context.Context) ([]entity.LotteryEntryID, error) {
	rows, err := r.db.Pool.Query(ctx, issuanceListAwaitingQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list lottery entries awaiting issuance")
	}
	defer rows.Close()

	var ids []entity.LotteryEntryID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, toAppErr(err, "failed to scan lottery entry id awaiting issuance")
		}
		ids = append(ids, entity.LotteryEntryID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating lottery entries awaiting issuance")
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
