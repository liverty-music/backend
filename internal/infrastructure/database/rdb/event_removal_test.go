package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
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

// TestRemoveEvents_ConcurrentPurchase covers "a purchase started while the
// delete runs is refused, never removed" (design D1): a checkout started
// between the blocker check and the removal fails on the removed ticket type;
// a removal started during a checkout or an Order issuance waits for it,
// without a deadlock, and then refuses.
func TestRemoveEvents_ConcurrentPurchase(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	insertHeld := `INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, status, hold_expire_at)
		VALUES ($1, $2, $3, 1, 3000, 1, now() + interval '15 minutes')`
	backendPID := func(t *testing.T, q interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}) uint32 {
		t.Helper()
		var pid uint32
		require.NoError(t, q.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
		return pid
	}
	// blocked reports whether the backend pid waits on a lock another
	// transaction holds.
	blocked := func(pid uint32) func() bool {
		return func() bool {
			var waits bool
			require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1)) > 0`, int32(pid)).Scan(&waits))
			return waits
		}
	}
	// blocking reports whether any backend waits on a lock the pid holds.
	blocking := func(pid uint32) func() bool {
		return func() bool {
			var holds bool
			require.NoError(t, testDB.Pool.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid)))`, int32(pid)).Scan(&holds))
			return holds
		}
	}

	t.Run("a checkout started during the removal is refused", func(t *testing.T) {
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		tt := seedTicketTypeForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		fan := seedFanUser(t)
		checkoutID := entity.NewID()

		removal, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = removal.Rollback(ctx) }()
		require.NoError(t, rdb.LockAndCheckEventRemoval(ctx, removal, []string{o.eventID}))

		// The checkout starts between the check and the removal, the window the
		// locks close.
		purchase, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = purchase.Rollback(ctx) }()
		pid := backendPID(t, purchase)
		inserted := make(chan error, 1)
		go func() {
			_, err := purchase.Exec(ctx, insertHeld, checkoutID, string(tt.ID), fan)
			inserted <- err
		}()
		require.Eventually(t, blocked(pid), 5*time.Second, 20*time.Millisecond, "the checkout waits for the removal")
		require.NoError(t, rdb.RemoveEvents(ctx, removal, []string{o.eventID}))
		require.NoError(t, removal.Commit(ctx))

		assert.True(t, rdb.IsForeignKeyViolation(<-inserted), "the checkout is refused on the removed ticket type")
		assert.False(t, rowExists(t, `SELECT 1 FROM checkouts WHERE id = $1`, checkoutID), "no checkout")
		assert.False(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "the event is removed")
	})

	t.Run("a removal started during a checkout waits and refuses", func(t *testing.T) {
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		tt := seedTicketTypeForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		checkoutID := entity.NewID()

		purchase, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = purchase.Rollback(ctx) }()
		_, err = purchase.Exec(ctx, insertHeld, checkoutID, string(tt.ID), seedFanUser(t))
		require.NoError(t, err)

		deleted := make(chan error, 1)
		go func() { deleted <- rdb.NewConcertRepository(testDB).DeleteAndSuppress(ctx, o.eventID) }()
		require.Eventually(t, blocking(backendPID(t, purchase)), 5*time.Second, 20*time.Millisecond, "the removal waits for the checkout")
		require.NoError(t, purchase.Commit(ctx))

		assert.ErrorIs(t, <-deleted, apperr.ErrFailedPrecondition, "the held checkout blocks the removal")
		assert.True(t, rowExists(t, `SELECT 1 FROM checkouts WHERE id = $1`, checkoutID), "the checkout remains")
		assert.True(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "the event remains")
	})

	t.Run("a removal started during an order issuance waits without a deadlock and refuses", func(t *testing.T) {
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		tt := seedTicketTypeForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		fan := seedFanUser(t)
		checkoutID, orderID := entity.NewID(), entity.NewID()
		_, err := testDB.Pool.Exec(ctx, `INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, card_authorization_id, status, hold_expire_at, commit_at, capture_at)
			VALUES ($1, $2, $3, 1, 3000, 'pi_issue', 2, now(), now(), now())`, checkoutID, string(tt.ID), fan)
		require.NoError(t, err)

		// The issuance's order: complete the checkout (locking its row), then,
		// once the removal waits, insert the Order and its Ticket, whose foreign
		// keys reach the checkout and the event.
		issuance, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = issuance.Rollback(ctx) }()
		_, err = issuance.Exec(ctx, `UPDATE checkouts SET status = 3 WHERE id = $1`, checkoutID)
		require.NoError(t, err)

		deleted := make(chan error, 1)
		go func() { deleted <- rdb.NewConcertRepository(testDB).DeleteAndSuppress(ctx, o.eventID) }()
		require.Eventually(t, blocking(backendPID(t, issuance)), 5*time.Second, 20*time.Millisecond, "the removal waits for the issuance")
		_, err = issuance.Exec(ctx, `INSERT INTO orders (id, user_id, checkout_id, provider, provider_payment_id, status, amount, currency, pay_at)
			VALUES ($1, $2, $3, 1, 'pi_issue', 1, 3000, 'JPY', now())`, orderID, fan, checkoutID)
		require.NoError(t, err)
		_, err = issuance.Exec(ctx, `INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at) VALUES ($1, $2, $3, $4, 1, now())`,
			entity.NewID(), orderID, fan, o.eventID)
		require.NoError(t, err, "the issuance is not chosen as a deadlock victim")
		require.NoError(t, issuance.Commit(ctx))

		assert.ErrorIs(t, <-deleted, apperr.ErrFailedPrecondition, "the paid order blocks the removal")
		assert.True(t, rowExists(t, `SELECT 1 FROM orders WHERE id = $1`, orderID), "the order remains")
		assert.True(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "the event remains")
	})
}
