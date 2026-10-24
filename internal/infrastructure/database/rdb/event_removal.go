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

const (
	// eventRemovalBlockersQuery reports, for the events bound to $1, whether
	// any has an Order that is not Refunded, a Settlement that is not Reversed,
	// or a purchase in progress: a Held or Committed checkout, an Expired or
	// Released checkout whose card authorization is not yet released, or an
	// Entered lottery entry.
	eventRemovalBlockersQuery = `
		SELECT
			EXISTS (
				SELECT 1 FROM orders o
				WHERE o.id IN (` + eventOrderIDsSubquery + `) AND o.status <> $2
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
				WHERE tt.event_id = ANY($1::uuid[]) AND le.state = $8
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
	// The purchase deletions below are filtered by status as well as by event:
	// a Paid Order committed after the blocker check is then left in place, and
	// the event delete fails on its RESTRICT foreign key instead of removing it.
	removeEventSettlementsQuery = `
		DELETE FROM settlements WHERE event_id = ANY($1::uuid[]) AND status = $2
	`
	removeEventTicketsQuery = `
		DELETE FROM tickets t USING orders o
		WHERE t.order_id = o.id AND t.event_id = ANY($1::uuid[]) AND o.status = $2
	`
	removeEventOrdersQuery = `
		DELETE FROM orders o
		WHERE o.id IN (` + eventOrderIDsSubquery + `) AND o.status = $2
	`
	// A checkout still referenced by an Order (one not refunded) makes this
	// delete fail on the RESTRICT foreign key instead of removing it.
	removeEventCheckoutsQuery = `
		DELETE FROM checkouts c USING ticket_types tt
		WHERE c.ticket_type_id = tt.id AND tt.event_id = ANY($1::uuid[])
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

// checkEventRemovalBlockers fails with FailedPrecondition when any of the
// events has a purchase that is still live: an Order that is not Refunded, a
// Settlement that is not Reversed, or a purchase in progress. It is the one
// blocker check for Concert deletion and Organizer deletion.
func checkEventRemovalBlockers(ctx context.Context, tx pgx.Tx, eventIDs []string) error {
	var unrefundedOrder, unreversedSettlement, purchaseInProgress bool
	if err := tx.QueryRow(ctx, eventRemovalBlockersQuery, eventIDs,
		int16(entity.OrderStatusRefunded), int16(entity.SettlementStatusReversed),
		int16(entity.CheckoutStatusHeld), int16(entity.CheckoutStatusCommitted),
		int16(entity.CheckoutStatusExpired), int16(entity.CheckoutStatusReleased),
		int16(entity.LotteryEntryStateEntered),
	).Scan(&unrefundedOrder, &unreversedSettlement, &purchaseInProgress); err != nil {
		return toAppErr(err, "failed to check event removal blockers")
	}
	switch {
	case unrefundedOrder:
		return apperr.New(codes.FailedPrecondition, "an event has an order that is not refunded")
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
// the Reversed Settlements, the Tickets and the Refunded Orders, the
// checkouts, then the events (which cascades their TicketTypes, lottery
// entries, concerts, performers and ticket journeys), then the TicketSales and
// first-party Series the removal leaves empty.
//
// The caller runs checkEventRemovalBlockers first in the same transaction; a
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

	refunded := int16(entity.OrderStatusRefunded)
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
		{removeEventTicketsQuery, []any{eventIDs, refunded}, "tickets"},
		{removeEventOrdersQuery, []any{eventIDs, refunded}, "orders"},
		{removeEventCheckoutsQuery, []any{eventIDs}, "checkouts"},
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
