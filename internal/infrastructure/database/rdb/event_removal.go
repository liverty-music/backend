package rdb

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// eventOrderIDsSubquery selects the ids of every Order of a TicketStock of the
// events bound to $1 (a uuid array).
const eventOrderIDsSubquery = `
	SELECT o.id FROM orders o
	JOIN ticket_stocks s ON s.id = o.ticket_stock_id
	WHERE s.event_id = ANY($1::uuid[])
`

// eventRemovalLockQueries lock, FOR UPDATE, the events' TicketStocks bound
// to $1, then their checkouts and lottery entries, then their Orders, then the
// events: the order every purchase path takes them in (the stock before the
// rows that change its counts, a checkout or entry before its Order), so the
// removal never deadlocks with one. A purchase that inserts an Order reaches
// its stock only through the stock's row lock or a foreign-key check (FOR KEY
// SHARE): one that inserts before the stocks are locked is waited for and
// then seen by the later statements and the blocker check; one that inserts
// after waits and then fails on the removed rows.
var eventRemovalLockQueries = []string{
	`SELECT 1 FROM ticket_stocks WHERE event_id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`,
	`SELECT 1 FROM checkouts c JOIN orders o ON o.id = c.order_id JOIN ticket_stocks s ON s.id = o.ticket_stock_id
	 WHERE s.event_id = ANY($1::uuid[]) ORDER BY c.id FOR UPDATE OF c`,
	`SELECT 1 FROM lottery_entries le JOIN orders o ON o.id = le.order_id JOIN ticket_stocks s ON s.id = o.ticket_stock_id
	 WHERE s.event_id = ANY($1::uuid[]) ORDER BY le.id FOR UPDATE OF le`,
	`SELECT 1 FROM orders o JOIN ticket_stocks s ON s.id = o.ticket_stock_id
	 WHERE s.event_id = ANY($1::uuid[]) ORDER BY o.id FOR UPDATE OF o`,
	`SELECT 1 FROM events WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`,
}

const (
	// eventRemovalBlockersQuery reports, for the events bound to $1, whether
	// any has a Paid Order, a Settlement that is not Reversed, or a purchase in
	// progress: a Pending Order, or a Void Order whose checkout has not
	// released its card authorization. A Lost entry's card authorization is
	// released by the draw before the outcome is recorded.
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
				SELECT 1 FROM orders o
				LEFT JOIN checkouts c ON c.order_id = o.id
				WHERE o.id IN (` + eventOrderIDsSubquery + `)
				  AND (o.status = $4
				       OR (o.status = $5 AND o.card_authorization_id IS NOT NULL
				           AND c.id IS NOT NULL AND c.card_authorization_release_at IS NULL))
			)
	`
	selectEventTicketSaleIDsQuery = `
		SELECT DISTINCT ticket_sale_id FROM ticket_sale_events WHERE event_id = ANY($1::uuid[])
	`
	selectEventSeriesIDsQuery = `
		SELECT DISTINCT series_id FROM events WHERE id = ANY($1::uuid[])
	`
	removeEventAdmissionsQuery     = `DELETE FROM admissions WHERE event_id = ANY($1::uuid[])`
	removeEventRejectedScansQuery  = `DELETE FROM rejected_scans WHERE event_id = ANY($1::uuid[])`
	removeEventScannersQuery       = `DELETE FROM scanners WHERE event_id = ANY($1::uuid[])`
	removeEventResaleListingsQuery = `
		DELETE FROM resale_listings rl USING ticket_stocks s
		WHERE rl.ticket_stock_id = s.id AND s.event_id = ANY($1::uuid[])
	`
	// The purchase deletions below are filtered by the Order statuses the
	// blocker check allows, as a second guard behind the locks: a row of any
	// other Order is left in place, and a RESTRICT foreign key then fails the
	// whole removal.
	removeEventSettlementsQuery = `
		DELETE FROM settlements WHERE event_id = ANY($1::uuid[]) AND status = $2
	`
	removeEventTicketsQuery = `
		DELETE FROM tickets t USING orders o
		WHERE t.order_id = o.id AND t.event_id = ANY($1::uuid[]) AND o.status = ANY($2::smallint[])
	`
	removeEventCheckoutsQuery = `
		DELETE FROM checkouts c USING orders o
		WHERE c.order_id = o.id AND o.id IN (` + eventOrderIDsSubquery + `) AND o.status = ANY($2::smallint[])
	`
	removeEventLotteryEntriesQuery = `
		DELETE FROM lottery_entries le USING orders o
		WHERE le.order_id = o.id AND o.id IN (` + eventOrderIDsSubquery + `) AND o.status = ANY($2::smallint[])
	`
	removeEventOrdersQuery = `
		DELETE FROM orders o
		WHERE o.id IN (` + eventOrderIDsSubquery + `) AND o.status = ANY($2::smallint[])
	`
	// removeEventsQuery cascades the events' ticket stocks and sale events.
	removeEventsQuery = `DELETE FROM events WHERE id = ANY($1::uuid[])`
	// removeEmptiedTicketSalesQuery removes the Organizer's TicketSales bound
	// to $1 that name no event any more.
	removeEmptiedTicketSalesQuery = `
		DELETE FROM ticket_sales ts
		WHERE ts.id = ANY($1::uuid[]) AND ts.discovered_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM ticket_sale_events tse WHERE tse.ticket_sale_id = ts.id)
	`
	// removeEmptiedFirstPartySeriesQuery removes the first-party Series bound to
	// $1 that have no event left, with their seat types. A discovered Series
	// stays.
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
		int16(entity.OrderStatusPending), int16(entity.OrderStatusVoid),
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
// rejected scans, Scanners), the resale listings of the events' TicketStocks,
// the Reversed Settlements, the Tickets, Checkouts and lottery entries of the
// Void and Refunded Orders and those Orders, then the events (which cascades
// their TicketStocks, sale events, concerts, performers and ticket journeys),
// then the Organizer's TicketSales and first-party Series the removal leaves
// empty.
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

	endedOrders := []int16{int16(entity.OrderStatusRefunded), int16(entity.OrderStatusVoid)}
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
		{removeEventCheckoutsQuery, []any{eventIDs, endedOrders}, "checkouts"},
		{removeEventLotteryEntriesQuery, []any{eventIDs, endedOrders}, "lottery entries"},
		{removeEventOrdersQuery, []any{eventIDs, endedOrders}, "orders"},
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
