package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// removableEvent is a CANCELLED first-party Event whose every purchase is
// finished: a row in every table that holds a key to the Event's records.
type removableEvent struct {
	deletableOrganizer
	// lottery is the Refunded Order of a Won lottery entry, with a Voided,
	// admitted Ticket and a Reversed Settlement.
	lottery seededPurchase
	// checkoutID is a Completed resale checkout with a Refunded Order and a
	// referral.
	checkoutID      string
	checkoutOrderID string
	checkoutTicket  string
	// resaleListingID is the Sold listing of the lottery Ticket, bought through
	// the checkout.
	resaleListingID string
}

// seedRemovableEvent seeds a removableEvent for an Organizer in the given
// status.
func seedRemovableEvent(t *testing.T, status entity.OrganizerStatus) removableEvent {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := testDB.Pool.Exec(ctx, query, args...)
		require.NoError(t, err)
	}

	e := removableEvent{deletableOrganizer: seedDeletableOrganizer(t, status)}
	exec(`UPDATE events SET publish_state = 'CANCELLED' WHERE id = $1`, e.eventID)
	e.lottery = seedPurchase(t, e.deletableOrganizer, entity.OrderStatusRefunded, entity.SettlementStatusReversed, true)
	exec(`INSERT INTO ticket_journeys (user_id, event_id, status) VALUES ($1, $2, 1)`, e.followerID, e.eventID)

	buyerID := seedFanUser(t)
	at := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	e.checkoutID, e.checkoutOrderID, e.checkoutTicket = entity.NewID(), entity.NewID(), entity.NewID()
	exec(`INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, card_authorization_id, status, hold_expire_at, commit_at, capture_at, resale)
		VALUES ($1, $2, $3, 1, 5000, $4, 3, $5, $5, $5, TRUE)`,
		e.checkoutID, string(e.typeID), buyerID, "pi_checkout_"+e.checkoutID, at)
	exec(`INSERT INTO referrals (id, checkout_id, code, created_at) VALUES ($1, $2, 'remove-test', $3)`,
		entity.NewID(), e.checkoutID, at)
	exec(`INSERT INTO orders (id, user_id, checkout_id, provider, provider_payment_id, status, amount, currency, pay_at)
		VALUES ($1, $2, $3, 1, $4, $5, 5000, 'JPY', $6)`,
		e.checkoutOrderID, buyerID, e.checkoutID, "pi_"+e.checkoutOrderID, int16(entity.OrderStatusRefunded), at)
	exec(`INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		e.checkoutTicket, e.checkoutOrderID, buyerID, e.eventID, int16(entity.TicketStatusVoided), at)
	e.resaleListingID = entity.NewID()
	exec(`INSERT INTO resale_listings (id, ticket_id, user_id, ticket_type_id, price, status, checkout_id, created_at, sold_at)
		VALUES ($1, $2, $3, $4, 5000, 2, $5, $6, $6)`,
		e.resaleListingID, e.lottery.ticketID, buyerID, string(e.typeID), e.checkoutID, at)
	return e
}

// eventRecordTablesQuery lists every table that holds a foreign key to the
// events table, or to a table that does, transitively.
const eventRecordTablesQuery = `
	WITH RECURSIVE fk AS (
		SELECT c.conrelid AS child, c.confrelid AS parent FROM pg_constraint c WHERE c.contype = 'f'
	), closure(rel) AS (
		SELECT 'events'::regclass::oid
		UNION
		SELECT fk.child FROM fk JOIN closure ON fk.parent = closure.rel
	)
	SELECT DISTINCT r.relname FROM closure JOIN pg_class r ON r.oid = closure.rel
	WHERE r.relname <> 'events'
	ORDER BY 1
`

// TestRemoveEvents_ForeignKeyCatalog fails when a table references an event's
// records but the removable fixture leaves it empty, so a new table with a
// RESTRICT key is caught here instead of failing a prod deletion. It then
// deletes the event and asserts every such table is empty again.
func TestRemoveEvents_ForeignKeyCatalog(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	cleanDatabase(t)
	e := seedRemovableEvent(t, entity.OrganizerStatusActive)

	rows, err := testDB.Pool.Query(ctx, eventRecordTablesQuery)
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, tables)

	count := func(table string) int {
		t.Helper()
		var n int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n))
		return n
	}
	for _, table := range tables {
		require.NotZerof(t, count(table),
			"%s references an event's records but the fixture has no row in it: add one to seedRemovableEvent and handle it in removeEvents", table)
	}

	require.NoError(t, rdb.NewConcertRepository(testDB).DeleteAndSuppress(ctx, e.eventID))

	for _, table := range tables {
		assert.Zerof(t, count(table), "%s keeps rows of the deleted event", table)
	}
	assert.False(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, e.eventID), "event")
	assert.Equal(t, 1, count("suppressed_concerts"), "the slot is suppressed")
}
