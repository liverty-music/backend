package rdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// IssuanceRepository implements entity.IssuanceRepository for PostgreSQL: it
// makes a Pending Order Paid and stores its N tickets and its Held settlement
// atomically in one transaction, and completes a source checkout.
type IssuanceRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.IssuanceRepository = (*IssuanceRepository)(nil)

const (
	// issuanceLockCheckoutQuery takes the row lock of the Order's Checkout,
	// if any, before the Order's: a checkout before its Order is the order
	// RevertCommit and Release take them in, so Issue never deadlocks with
	// them.
	issuanceLockCheckoutQuery = `SELECT id FROM checkouts WHERE order_id = $1 FOR UPDATE`
	// issuanceLockOrderQuery takes the Order's row lock, which every status
	// change of the Order takes, and reads its status. Under READ COMMITTED a
	// row lock that waited reads the row as the transaction it waited for
	// left it.
	issuanceLockOrderQuery = `SELECT status FROM orders WHERE id = $1 FOR UPDATE`
	// issuancePayOrderQuery makes the Pending Order Paid with its captured
	// payment.
	issuancePayOrderQuery = `
		UPDATE orders SET status = 1, provider = $2, provider_payment_id = $3, provider_payment_method_id = $4,
			card_brand = $5, card_last4 = $6, pay_at = $7
		WHERE id = $1 AND status = 4
	`
	issuanceInsertTicketQuery = `
		INSERT INTO tickets (
			id, order_id, user_id, event_id, identity_verification_id,
			resale_without_consent_prohibited, status, issue_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	// issuanceInsertSettlementQuery inserts the Held settlement row for the
	// issued Order. Settlement.organizer_id/event_id are NOT NULL, so the
	// caller (OrderUseCase) must resolve the Organizer before calling Issue.
	issuanceInsertSettlementQuery = `
		INSERT INTO settlements (id, order_id, organizer_id, event_id, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	// issuanceInsertSettlementSplitQuery inserts one payout split for the
	// settlement just created. provider_payout_id/provider_payout_reversal_id default to
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
	// issuanceListAwaitingQuery returns the Won (state 2) lottery entries
	// whose Order is Pending (status 4): the issuance sweeper work-list.
	issuanceListAwaitingQuery = `
		SELECT le.id
		FROM lottery_entries le
		JOIN orders o ON o.id = le.order_id
		WHERE le.state = 2 AND o.status = 4
		ORDER BY le.id
	`
)

// NewIssuanceRepository creates a new issuance repository instance.
func NewIssuanceRepository(db *Database) *IssuanceRepository {
	return &IssuanceRepository{db: db}
}

// Issue makes the Pending Order Paid and stores its N tickets and its Held
// settlement (with splits), and completes its source Checkout, in one
// transaction under the Order's row lock. An Order already Paid or Refunded
// surfaces as AlreadyExists so the caller re-reads it instead of issuing
// twice.
//
// settlement is required (non-nil); the caller resolves the event's Organizer
// before calling Issue so the Order is never Paid without its payout record.
func (r *IssuanceRepository) Issue(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error {
	attrs := []slog.Attr{slog.String("order_id", string(order.ID))}
	if !order.CanBePaid() {
		return apperr.New(codes.InvalidArgument, "a paid order needs its card authorization, provider payment ID and pay time", attrs...)
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin issuance transaction", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status     int16
		checkoutID sql.NullString
	)
	err = tx.QueryRow(ctx, issuanceLockCheckoutQuery, string(order.ID)).Scan(&checkoutID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return toAppErr(err, "failed to lock the order's checkout", attrs...)
	}
	if err := tx.QueryRow(ctx, issuanceLockOrderQuery, string(order.ID)).Scan(&status); err != nil {
		return toAppErr(err, "failed to lock the order", attrs...)
	}
	switch entity.OrderStatus(status) {
	case entity.OrderStatusPaid, entity.OrderStatusRefunded:
		return apperr.New(codes.AlreadyExists, "the order is already paid", attrs...)
	case entity.OrderStatusVoid:
		return apperr.New(codes.FailedPrecondition, "the order is void", attrs...)
	}

	// A checkout is completed first, so an uncharged one stores nothing.
	if checkoutID.Valid {
		tag, err := tx.Exec(ctx, issuanceCompleteCheckoutQuery, checkoutID.String)
		if err != nil {
			return toAppErr(err, "failed to complete checkout", append(attrs, slog.String("checkout_id", checkoutID.String))...)
		}
		if tag.RowsAffected() == 0 {
			return apperr.New(codes.FailedPrecondition, "checkout is not Committed and charged",
				append(attrs, slog.String("checkout_id", checkoutID.String))...)
		}
	}

	if _, err := tx.Exec(ctx, issuancePayOrderQuery,
		string(order.ID), int16(order.Payment.Provider), order.Payment.ProviderPaymentID, order.Payment.ProviderPaymentMethodID,
		order.Payment.CardBrand, order.Payment.CardLast4, order.PayTime,
	); err != nil {
		return toAppErr(err, "failed to make the order paid", attrs...)
	}

	for _, t := range tickets {
		if _, err := tx.Exec(ctx, issuanceInsertTicketQuery,
			string(t.ID), string(t.OrderID), string(t.UserID), t.EventID,
			nullableUUID(t.IdentityVerificationID), t.ResaleWithoutConsentProhibited,
			int16(t.Status), t.IssueTime,
		); err != nil {
			return toAppErr(err, "failed to insert ticket", slog.String("ticket_id", string(t.ID)))
		}
	}

	if _, err := tx.Exec(ctx, issuanceInsertSettlementQuery,
		string(settlement.ID), string(settlement.OrderID), settlement.OrganizerID, settlement.EventID,
		int16(settlement.Status), settlement.CreateTime,
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
		return toAppErr(err, "failed to commit issuance transaction", attrs...)
	}

	r.db.logger.Info(ctx, "issued order and tickets",
		slog.String("entityType", "orders"),
		slog.String("orderID", string(order.ID)),
		slog.String("lotteryEntryID", string(order.LotteryEntryID)),
		slog.String("checkoutID", checkoutID.String),
		slog.Int("ticketCount", len(tickets)),
	)
	return nil
}

// ListLotteryEntryIDsAwaitingIssuance returns the Won lottery entries whose
// Order is Pending.
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
