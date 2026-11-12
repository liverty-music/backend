package rdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// IssuanceRepository implements entity.IssuanceRepository for PostgreSQL: it
// makes a Pending Order Paid and stores its N tickets and its Held settlement
// atomically in one transaction, and completes a source checkout.
type IssuanceRepository struct {
	db     *Database
	outbox Announcer
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
	// issuanceResaleEntryQuery reads the resale entry that names the Order,
	// if any, and its Order's stock.
	issuanceResaleEntryQuery = `
		SELECT re.id, o.ticket_stock_id FROM resale_entries re JOIN orders o ON o.id = re.order_id WHERE re.order_id = $1
	`
	// issuanceLockResaleEntryQuery locks the resale entry and reads its state.
	issuanceLockResaleEntryQuery = `SELECT state FROM resale_entries WHERE id = $1 FOR UPDATE`
	// issuanceLockResoldTicketsQuery locks, in id order, the tickets of the
	// Listed listings allocated to the entry. Rows are locked tickets first,
	// then listings, then settlements, as CommitRefund does, so the two never
	// deadlock.
	issuanceLockResoldTicketsQuery = `
		SELECT t.id FROM tickets t
		JOIN resale_listings rl ON rl.ticket_id = t.id
		WHERE rl.resale_entry_id = $1 AND rl.status = 1
		ORDER BY t.id
		FOR UPDATE OF t
	`
	// issuanceSellListingsQuery sells the Listed listings allocated to the
	// entry and returns each listing, its ticket and the ticket's order.
	// issuanceCancelledListingQuery reports whether a listing allocated to
	// the entry is Cancelled, as an Order's refund leaves it.
	issuanceCancelledListingQuery = `
		SELECT EXISTS (SELECT 1 FROM resale_listings WHERE resale_entry_id = $1 AND status = 4)
	`
	issuanceSellListingsQuery = `
		UPDATE resale_listings rl SET status = 2, sold_at = $2
		FROM tickets t
		WHERE rl.resale_entry_id = $1 AND rl.status = 1 AND t.id = rl.ticket_id
		RETURNING rl.id, rl.ticket_id, t.order_id
	`
	// issuanceIssuedTicketsQuery counts the order's tickets that are still
	// Issued. It runs under the order's settlement lock, so a concurrent
	// resale of the same order has either committed its voids or not started.
	issuanceIssuedTicketsQuery = `SELECT count(*)::int FROM tickets WHERE order_id = $1 AND status = 1`
	issuanceVoidTicketsQuery   = `UPDATE tickets SET status = 2 WHERE id = ANY($1::uuid[]) AND status = 1`
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
)

// NewIssuanceRepository creates a new issuance repository instance.
func NewIssuanceRepository(db *Database, outbox Announcer) *IssuanceRepository {
	return &IssuanceRepository{db: db, outbox: outbox}
}

// Issue makes the Pending Order Paid and stores its N tickets and its Held
// settlement (with splits), and completes its source Checkout, in one
// transaction under the Order's row lock. An Order already Paid or Refunded
// surfaces as AlreadyExists so the caller re-reads it instead of issuing
// twice.
//
// settlement is required (non-nil); the caller resolves the event's Organizer
// before calling Issue so the Order is never Paid without its payout record.
//
// # Announces
//
// ORDER.paid with the Order as Paid, in the transaction of the issuance.
// Nothing when Issue fails, including with AlreadyExists.
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
		status                 int16
		checkoutID             sql.NullString
		resaleEntryID, stockID string
	)
	// A won resale entry's stock is locked before its Order, as the draw and
	// RevertWin lock the stock before the entry and its Order.
	err = tx.QueryRow(ctx, issuanceResaleEntryQuery, string(order.ID)).Scan(&resaleEntryID, &stockID)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx, ticketStockLockQuery, stockID); err != nil {
			return toAppErr(err, "failed to lock the resale entry's stock", attrs...)
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return toAppErr(err, "failed to read the order's resale entry", attrs...)
	}
	err = tx.QueryRow(ctx, issuanceLockCheckoutQuery, string(order.ID)).Scan(&checkoutID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return toAppErr(err, "failed to lock the order's checkout", attrs...)
	}
	if err := tx.QueryRow(ctx, issuanceLockOrderQuery, string(order.ID)).Scan(&status); err != nil {
		return toAppErr(err, "failed to lock the order", attrs...)
	}
	if err := (&entity.Order{Status: entity.OrderStatus(status)}).IssuanceOutcome(); err != nil {
		return err
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

	var sold []*entity.ResaleListing
	if resaleEntryID != "" {
		if sold, err = sellResaleListings(ctx, tx, resaleEntryID, order); err != nil {
			return err
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

	paid := *order
	paid.Status = entity.OrderStatusPaid
	if err := announce(ctx, r.outbox, tx, entity.SubjectOrderPaid, &paid); err != nil {
		return err
	}
	for _, l := range sold {
		if err := announce(ctx, r.outbox, tx, entity.SubjectResaleListingSold, l); err != nil {
			return err
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

// sellResaleListings, for a won resale entry, sells the listings allocated to
// it with the order's pay time, voids their tickets and takes the share of the
// resold tickets off the Held settlement of each original order, inside tx,
// and returns the listings as Sold. When a listing allocated to it is
// Cancelled it fails with the reason of [entity.ErrResaleListingCancelled]. It locks the
// listed tickets first, as CommitRefund locks an Order's tickets, so of a
// refund of the listing fan's Order and this sell the one that commits second
// sees the other's result.
func sellResaleListings(ctx context.Context, tx pgx.Tx, entryID string, order *entity.Order) ([]*entity.ResaleListing, error) {
	attrs := slog.String("resale_entry_id", entryID)
	var state int16
	if err := tx.QueryRow(ctx, issuanceLockResaleEntryQuery, entryID).Scan(&state); err != nil {
		return nil, toAppErr(err, "failed to lock the resale entry", attrs)
	}
	if entity.ResaleEntryState(state) != entity.ResaleEntryStateWon {
		return nil, apperr.New(codes.FailedPrecondition, "the resale entry is not won", attrs)
	}
	if _, err := tx.Exec(ctx, issuanceLockResoldTicketsQuery, entryID); err != nil {
		return nil, toAppErr(err, "failed to lock resold tickets", attrs)
	}
	rows, err := tx.Query(ctx, issuanceSellListingsQuery, entryID, order.PayTime)
	if err != nil {
		return nil, toAppErr(err, "failed to sell resale listings", attrs)
	}
	var listingIDs, ticketIDs []string
	resold := map[string]int{} // original order id -> tickets resold now
	for rows.Next() {
		var listingID, ticketID, orderID string
		if err := rows.Scan(&listingID, &ticketID, &orderID); err != nil {
			rows.Close()
			return nil, toAppErr(err, "failed to scan a sold listing", attrs)
		}
		listingIDs = append(listingIDs, listingID)
		ticketIDs = append(ticketIDs, ticketID)
		resold[orderID]++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to sell resale listings", attrs)
	}
	if len(ticketIDs) != order.TicketCount {
		// A refund of the listing fan's Order that committed first has
		// cancelled the listing; the sell leaves it and reports it.
		var cancelled bool
		if err := tx.QueryRow(ctx, issuanceCancelledListingQuery, entryID).Scan(&cancelled); err != nil {
			return nil, toAppErr(err, "failed to read the resale entry's cancelled listings", attrs)
		}
		if cancelled {
			return nil, apperr.Wrap(entity.ErrResaleListingCancelled, codes.FailedPrecondition, "a listing allocated to the resale entry was cancelled", attrs)
		}
		return nil, apperr.New(codes.FailedPrecondition, "the resale entry is not allocated a listing for every ticket",
			attrs, slog.Int("ticket_count", order.TicketCount), slog.Int("listings", len(ticketIDs)))
	}

	// Lock each original settlement in a fixed order and count the order's
	// Issued tickets under that lock, before this resale voids any of them.
	orderIDs := make([]string, 0, len(resold))
	for id := range resold {
		orderIDs = append(orderIDs, id)
	}
	slices.Sort(orderIDs)
	settlements := make([]*entity.Settlement, len(orderIDs))
	issued := make([]int, len(orderIDs))
	for i, id := range orderIDs {
		if settlements[i], err = lockSettlement(ctx, tx, id); err != nil {
			return nil, err
		}
		if err := tx.QueryRow(ctx, issuanceIssuedTicketsQuery, id).Scan(&issued[i]); err != nil {
			return nil, toAppErr(err, "failed to count the original order's tickets", slog.String("order_id", id))
		}
	}
	tag, err := tx.Exec(ctx, issuanceVoidTicketsQuery, ticketIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to void resold tickets", attrs)
	}
	if int(tag.RowsAffected()) != len(ticketIDs) {
		return nil, apperr.New(codes.FailedPrecondition, "a resold ticket is no longer Issued", attrs)
	}
	for i, id := range orderIDs {
		if err := giveUpResoldShare(ctx, tx, settlements[i], id, resold[id], issued[i]); err != nil {
			return nil, err
		}
	}
	sold := make([]*entity.ResaleListing, 0, len(listingIDs))
	for _, id := range listingIDs {
		l, err := scanResaleListing(tx.QueryRow(ctx, resaleListingGetQuery, id))
		if err != nil {
			return nil, toAppErr(err, "failed to read a sold listing", attrs)
		}
		sold = append(sold, l)
	}
	return sold, nil
}

// lockSettlement locks the settlement of the order inside tx and reads it with
// its splits.
func lockSettlement(ctx context.Context, tx pgx.Tx, orderID string) (*entity.Settlement, error) {
	attrs := slog.String("order_id", orderID)
	var id string
	var status int16
	if err := tx.QueryRow(ctx, issuanceLockSettlementQuery, orderID).Scan(&id, &status); err != nil {
		return nil, toAppErr(err, "failed to lock the original order's settlement", attrs)
	}
	rows, err := tx.Query(ctx, issuanceSplitsQuery, id)
	if err != nil {
		return nil, toAppErr(err, "failed to read the original settlement's splits", attrs)
	}
	splits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.SettlementSplit, error) {
		var split entity.SettlementSplit
		err := row.Scan(&split.PayeeOrganizerID, &split.Amount)
		return split, err
	})
	if err != nil {
		return nil, toAppErr(err, "failed to read the original settlement's splits", attrs)
	}
	return &entity.Settlement{ID: entity.SettlementID(id), Status: entity.SettlementStatus(status), Splits: splits}, nil
}

// giveUpResoldShare takes the share of k resold tickets, of the m that were
// Issued, off the locked Held settlement of the original order, inside tx.
func giveUpResoldShare(ctx context.Context, tx pgx.Tx, settlement *entity.Settlement, orderID string, k, m int) error {
	attrs := slog.String("order_id", orderID)
	id := string(settlement.ID)
	if err := settlement.GiveUpResoldShare(k, m); err != nil {
		return err
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

// nullableUUID returns nil for an empty id so a NULL is written to a nullable
// UUID column, or the id string otherwise.
func nullableUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
