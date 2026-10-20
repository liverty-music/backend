package rdb

import (
	"context"
	"log/slog"
	"sort"

	"github.com/jackc/pgx/v5"
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
	issuanceCheckoutHasOrderQuery = `SELECT EXISTS (SELECT 1 FROM orders WHERE checkout_id = $1)`
	issuanceCheckoutKindQuery     = `SELECT resale, ticket_count FROM checkouts WHERE id = $1`
	// issuanceSellListingsQuery sells the Listed listings the checkout holds
	// and returns the ticket and the original order of each.
	issuanceSellListingsQuery = `
		UPDATE resale_listings rl SET status = 2, sold_at = $2
		FROM tickets t
		WHERE rl.checkout_id = $1 AND rl.status = 1 AND t.id = rl.ticket_id
		RETURNING rl.ticket_id, t.order_id
	`
	// issuanceIssuedTicketsQuery counts, per order in $1, its tickets that are
	// still Issued, before the resold ones are voided.
	issuanceIssuedTicketsQuery = `
		SELECT order_id, count(*)::int FROM tickets
		WHERE order_id = ANY($1::uuid[]) AND status = 1
		GROUP BY order_id
	`
	issuanceVoidTicketsQuery = `UPDATE tickets SET status = 2 WHERE id = ANY($1::uuid[]) AND status = 1`
	// issuanceLockSettlementQuery locks the settlement of an original order.
	issuanceLockSettlementQuery = `SELECT id, status FROM settlements WHERE order_id = $1 FOR UPDATE`
	issuanceSplitsQuery         = `
		SELECT payee_organizer_id, amount FROM settlement_splits
		WHERE settlement_id = $1 ORDER BY payee_organizer_id
	`
	issuanceSetSettlementStatusQuery = `UPDATE settlements SET status = $2 WHERE id = $1`
	issuanceSetSplitAmountQuery      = `
		UPDATE settlement_splits SET amount = $3
		WHERE settlement_id = $1 AND payee_organizer_id = $2
	`
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

	if order.CheckoutID != "" {
		if err := sellResaleListings(ctx, tx, order); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, issuanceInsertOrderQuery,
		string(order.ID), string(order.UserID), nullableUUID(string(order.LotteryEntryID)),
		nullableUUID(string(order.CheckoutID)), int16(order.Payment.Provider), order.Payment.ProviderPaymentID, order.Payment.ProviderPaymentMethodID,
		order.Payment.CardBrand, order.Payment.CardLast4,
		int16(order.Status), order.Amount, order.Currency, order.PayTime,
	); err != nil {
		return toAppErr(err, "failed to insert order",
			slog.String("lottery_entry_id", string(order.LotteryEntryID)),
			slog.String("checkout_id", string(order.CheckoutID)))
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

// sellResaleListings, for a resale checkout, sells the listings it holds with
// the order's pay time, voids their tickets and takes the share of the resold
// tickets off the Held settlement of each original order, inside tx. A stock
// checkout changes nothing.
func sellResaleListings(ctx context.Context, tx pgx.Tx, order *entity.Order) error {
	attrs := slog.String("checkout_id", string(order.CheckoutID))
	var (
		resale bool
		count  int
	)
	if err := tx.QueryRow(ctx, issuanceCheckoutKindQuery, string(order.CheckoutID)).Scan(&resale, &count); err != nil {
		return toAppErr(err, "failed to read the checkout's kind", attrs)
	}
	if !resale {
		return nil
	}

	rows, err := tx.Query(ctx, issuanceSellListingsQuery, string(order.CheckoutID), order.PayTime)
	if err != nil {
		return toAppErr(err, "failed to sell resale listings", attrs)
	}
	var ticketIDs []string
	resold := map[string]int{} // original order id -> tickets resold now
	for rows.Next() {
		var ticketID, orderID string
		if err := rows.Scan(&ticketID, &orderID); err != nil {
			rows.Close()
			return toAppErr(err, "failed to scan a sold listing", attrs)
		}
		ticketIDs = append(ticketIDs, ticketID)
		resold[orderID]++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return toAppErr(err, "failed to sell resale listings", attrs)
	}
	if len(ticketIDs) != count {
		return apperr.New(codes.FailedPrecondition, "the resale checkout no longer holds a listing for every ticket",
			attrs, slog.Int("ticket_count", count), slog.Int("listings", len(ticketIDs)))
	}

	orderIDs := make([]string, 0, len(resold))
	for id := range resold {
		orderIDs = append(orderIDs, id)
	}
	sort.Strings(orderIDs) // lock settlements in a fixed order
	issued := map[string]int{}
	rows, err = tx.Query(ctx, issuanceIssuedTicketsQuery, orderIDs)
	if err != nil {
		return toAppErr(err, "failed to count the original orders' tickets", attrs)
	}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return toAppErr(err, "failed to scan an original order's tickets", attrs)
		}
		issued[id] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return toAppErr(err, "failed to count the original orders' tickets", attrs)
	}

	tag, err := tx.Exec(ctx, issuanceVoidTicketsQuery, ticketIDs)
	if err != nil {
		return toAppErr(err, "failed to void resold tickets", attrs)
	}
	if int(tag.RowsAffected()) != len(ticketIDs) {
		return apperr.New(codes.FailedPrecondition, "a resold ticket is no longer Issued", attrs)
	}

	for _, id := range orderIDs {
		if err := giveUpResoldShare(ctx, tx, id, resold[id], issued[id]); err != nil {
			return err
		}
	}
	return nil
}

// giveUpResoldShare takes the share of k resold tickets, of the m that were
// Issued, off the Held settlement of the original order, inside tx.
func giveUpResoldShare(ctx context.Context, tx pgx.Tx, orderID string, k, m int) error {
	attrs := slog.String("order_id", orderID)
	settlement := &entity.Settlement{}
	var id string
	var status int16
	if err := tx.QueryRow(ctx, issuanceLockSettlementQuery, orderID).Scan(&id, &status); err != nil {
		return toAppErr(err, "failed to lock the original order's settlement", attrs)
	}
	settlement.ID = entity.SettlementID(id)
	settlement.Status = entity.SettlementStatus(status)
	rows, err := tx.Query(ctx, issuanceSplitsQuery, id)
	if err != nil {
		return toAppErr(err, "failed to read the original settlement's splits", attrs)
	}
	settlement.Splits, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.SettlementSplit, error) {
		var split entity.SettlementSplit
		err := row.Scan(&split.PayeeOrganizerID, &split.Amount)
		return split, err
	})
	if err != nil {
		return toAppErr(err, "failed to read the original settlement's splits", attrs)
	}

	if err := settlement.GiveUpResoldShare(k, m); err != nil {
		return apperr.Wrap(err, codes.FailedPrecondition, "the original settlement cannot give up the resold share", attrs)
	}
	if settlement.Status == entity.SettlementStatusReversed {
		if _, err := tx.Exec(ctx, issuanceSetSettlementStatusQuery, id, int16(settlement.Status)); err != nil {
			return toAppErr(err, "failed to reverse the original settlement", attrs)
		}
		return nil
	}
	for _, split := range settlement.Splits {
		if _, err := tx.Exec(ctx, issuanceSetSplitAmountQuery, id, split.PayeeOrganizerID, split.Amount); err != nil {
			return toAppErr(err, "failed to lower the original settlement's split", attrs)
		}
	}
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
