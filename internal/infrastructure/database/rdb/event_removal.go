package rdb

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// eventOrderIDsSubquery selects the ids of every Order whose source, a lottery
// entry or a checkout, is for a TicketType of the events bound to $1 (a uuid
// array).
const eventOrderIDsSubquery = `
	SELECT o.id FROM orders o
	JOIN lottery_entries le ON le.id = o.lottery_entry_id
	JOIN ticket_types tt ON tt.id = le.ticket_type_id
	WHERE tt.event_id = ANY($1::uuid[])
	UNION ALL
	SELECT o.id FROM orders o
	JOIN checkouts c ON c.id = o.checkout_id
	JOIN ticket_types tt ON tt.id = c.ticket_type_id
	WHERE tt.event_id = ANY($1::uuid[])
`

// eventRemovalLockQueries lock, FOR UPDATE and child rows first, the
// checkouts and lottery entries of the ticket types of the events bound to $1,
// then those ticket types, then the events. The purchase paths take the same
// order: creating, moving forward or issuing a checkout or lottery entry locks
// that row first and reaches its ticket type or event only through a
// foreign-key check (FOR KEY SHARE) when it inserts. A purchase that inserts
// before the parent locks is waited for and then seen by the blocker check; one
// that inserts after them waits and then fails on the removed rows.
var eventRemovalLockQueries = []string{
	`SELECT 1 FROM checkouts c JOIN ticket_types tt ON tt.id = c.ticket_type_id
	 WHERE tt.event_id = ANY($1::uuid[]) ORDER BY c.id FOR UPDATE OF c`,
	`SELECT 1 FROM lottery_entries le JOIN ticket_types tt ON tt.id = le.ticket_type_id
	 WHERE tt.event_id = ANY($1::uuid[]) ORDER BY le.id FOR UPDATE OF le`,
	`SELECT 1 FROM ticket_types WHERE event_id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`,
	`SELECT 1 FROM events WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`,
}

const (
	// eventRemovalBlockersQuery reports, for the events bound to $1, whether
	// any has a Paid Order, a Settlement that is not Reversed, or a purchase in
	// progress: a Held or Committed checkout, an Expired or Released checkout
	// whose card authorization is not yet released, an Entered lottery entry, a
	// Draft lottery entry whose card authorization is open, or a Won lottery
	// entry whose Order is not yet issued (its payment is captured).
	eventRemovalBlockersQuery = `
		SELECT
			EXISTS (
				SELECT 1 FROM orders o
				WHERE o.id IN (` + eventOrderIDsSubquery + `) AND o.status = $2
			),
			EXISTS (
				SELECT 1 FROM settlements st
				WHERE st.event_id = ANY($1::uuid[]) AND st.status <> $3
			),
			EXISTS (
				SELECT 1 FROM checkouts c
				JOIN ticket_types tt ON tt.id = c.ticket_type_id
				WHERE tt.event_id = ANY($1::uuid[])
				  AND (c.status IN ($4, $5)
				       OR (c.status IN ($6, $7) AND c.card_authorization_id IS NOT NULL AND c.card_authorization_release_at IS NULL))
			) OR EXISTS (
				SELECT 1 FROM lottery_entries le
				JOIN ticket_types tt ON tt.id = le.ticket_type_id
				WHERE tt.event_id = ANY($1::uuid[])
				  AND (le.state = $8
				       OR (le.state = $9 AND le.card_authorization_id IS NOT NULL)
				       OR (le.state = $10 AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.lottery_entry_id = le.id)))
			)
	`
	selectEventTicketSaleIDsQuery = `
		SELECT DISTINCT ticket_sale_id FROM ticket_types WHERE event_id = ANY($1::uuid[])
	`
	selectEventSeriesIDsQuery = `
		SELECT DISTINCT series_id FROM events WHERE id = ANY($1::uuid[])
	`
	removeEventAdmissionsQuery     = `DELETE FROM admissions WHERE event_id = ANY($1::uuid[])`
	removeEventRejectedScansQuery  = `DELETE FROM rejected_scans WHERE event_id = ANY($1::uuid[])`
	removeEventScannersQuery       = `DELETE FROM scanners WHERE event_id = ANY($1::uuid[])`
	removeEventResaleListingsQuery = `
		DELETE FROM resale_listings rl USING ticket_types tt
		WHERE rl.ticket_type_id = tt.id AND tt.event_id = ANY($1::uuid[])
	`
	// The purchase deletions below are filtered by the states the blocker check
	// allows, as a second guard behind the locks: a row in any other state is
	// left in place, and a RESTRICT foreign key then fails the whole removal.
	removeEventSettlementsQuery = `
		DELETE FROM settlements WHERE event_id = ANY($1::uuid[]) AND status = $2
	`
	removeEventTicketsQuery = `
		DELETE FROM tickets t USING orders o
		WHERE t.order_id = o.id AND t.event_id = ANY($1::uuid[]) AND o.status = ANY($2::smallint[])
	`
	removeEventOrdersQuery = `
		DELETE FROM orders o
		WHERE o.id IN (` + eventOrderIDsSubquery + `) AND o.status = ANY($2::smallint[])
	`
	removeEventCheckoutsQuery = `
		DELETE FROM checkouts c USING ticket_types tt
		WHERE c.ticket_type_id = tt.id AND tt.event_id = ANY($1::uuid[]) AND c.status = ANY($2::smallint[])
	`
	removeEventsQuery = `DELETE FROM events WHERE id = ANY($1::uuid[])`
	// removeEmptiedTicketSalesQuery removes the TicketSales bound to $1 that no
	// TicketType offers any more.
	removeEmptiedTicketSalesQuery = `
		DELETE FROM ticket_sales ts
		WHERE ts.id = ANY($1::uuid[])
		  AND NOT EXISTS (SELECT 1 FROM ticket_types tt WHERE tt.ticket_sale_id = ts.id)
	`
	// removeEmptiedFirstPartySeriesQuery removes the first-party Series bound to
	// $1 that have no event left. A discovered Series stays.
	removeEmptiedFirstPartySeriesQuery = `
		DELETE FROM series s
		WHERE s.id = ANY($1::uuid[]) AND s.organizer_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.series_id = s.id)
	`
)

// lockAndCheckEventRemoval takes the row locks of eventRemovalLockQueries,
// which hold until tx ends, and then fails with FailedPrecondition when any of
// the events has a purchase that is still live: a Paid Order, a Settlement that
// is not Reversed, or a purchase in progress. It is the one entry point for the
// locks and the check of Concert deletion and Organizer deletion, so neither
// can check without locking; a purchase started after the check is refused
// rather than removed.
func lockAndCheckEventRemoval(ctx context.Context, tx pgx.Tx, eventIDs []string) error {
	for _, q := range eventRemovalLockQueries {
		if _, err := tx.Exec(ctx, q, eventIDs); err != nil {
			return toAppErr(err, "failed to lock the events' purchase records")
		}
	}
	var paidOrder, unreversedSettlement, purchaseInProgress bool
	if err := tx.QueryRow(ctx, eventRemovalBlockersQuery, eventIDs,
		int16(entity.OrderStatusPaid), int16(entity.SettlementStatusReversed),
		int16(entity.CheckoutStatusHeld), int16(entity.CheckoutStatusCommitted),
		int16(entity.CheckoutStatusExpired), int16(entity.CheckoutStatusReleased),
		int16(entity.LotteryEntryStateEntered), int16(entity.LotteryEntryStateDraft),
		int16(entity.LotteryEntryStateWon),
	).Scan(&paidOrder, &unreversedSettlement, &purchaseInProgress); err != nil {
		return toAppErr(err, "failed to check event removal blockers")
	}
	switch {
	case paidOrder:
		return apperr.New(codes.FailedPrecondition, "an event has a paid order")
	case unreversedSettlement:
		return apperr.New(codes.FailedPrecondition, "an event has a settlement that is not reversed")
	case purchaseInProgress:
		return apperr.New(codes.FailedPrecondition, "an event has a purchase in progress")
	}
	return nil
}

// removeEvents removes the events and every record that depends only on them,
// within tx, in RESTRICT-safe order: the admission records (admissions,
// rejected scans, Scanners), the resale listings of the events' TicketTypes,
// the Reversed Settlements, the Tickets and the Refunded and Failed Orders,
// the Completed, Expired and Released checkouts, then the events (which cascades their TicketTypes, lottery
// entries, concerts, performers and ticket journeys), then the TicketSales and
// first-party Series the removal leaves empty.
//
// The caller runs lockAndCheckEventRemoval first in the same transaction; a
// live purchase left in place makes a RESTRICT foreign key fail the removal.
func removeEvents(ctx context.Context, tx pgx.Tx, eventIDs []string) error {
	saleIDs, err := collectIDs(ctx, tx, selectEventTicketSaleIDsQuery, eventIDs)
	if err != nil {
		return toAppErr(err, "failed to read the events' ticket sales")
	}
	seriesIDs, err := collectIDs(ctx, tx, selectEventSeriesIDsQuery, eventIDs)
	if err != nil {
		return toAppErr(err, "failed to read the events' series")
	}

	endedOrders := []int16{int16(entity.OrderStatusRefunded), int16(entity.OrderStatusFailed)}
	endedCheckouts := []int16{
		int16(entity.CheckoutStatusCompleted), int16(entity.CheckoutStatusExpired), int16(entity.CheckoutStatusReleased),
	}
	statements := []struct {
		query string
		args  []any
		what  string
	}{
		{removeEventAdmissionsQuery, []any{eventIDs}, "admissions"},
		{removeEventRejectedScansQuery, []any{eventIDs}, "rejected scans"},
		{removeEventScannersQuery, []any{eventIDs}, "scanners"},
		{removeEventResaleListingsQuery, []any{eventIDs}, "resale listings"},
		{removeEventSettlementsQuery, []any{eventIDs, int16(entity.SettlementStatusReversed)}, "settlements"},
		{removeEventTicketsQuery, []any{eventIDs, endedOrders}, "tickets"},
		{removeEventOrdersQuery, []any{eventIDs, endedOrders}, "orders"},
		{removeEventCheckoutsQuery, []any{eventIDs, endedCheckouts}, "checkouts"},
		{removeEventsQuery, []any{eventIDs}, "events"},
		{removeEmptiedTicketSalesQuery, []any{saleIDs}, "ticket sales"},
		{removeEmptiedFirstPartySeriesQuery, []any{seriesIDs}, "series"},
	}
	for _, st := range statements {
		if _, err := tx.Exec(ctx, st.query, st.args...); err != nil {
			return toAppErr(err, "failed to remove the events' "+st.what, slog.Int("events", len(eventIDs)))
		}
	}
	return nil
}

// collectIDs runs a query returning one uuid column and collects it as strings.
func collectIDs(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
