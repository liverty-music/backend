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
// delete runs is refused, never removed": an application started between the
// blocker check and the removal fails on the removed ticket stock; a removal
// started during a checkout or an Order issuance waits for it, without a
// deadlock, and then refuses.
func TestRemoveEvents_ConcurrentPurchase(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	insertPending := `INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, provider, status, amount, currency)
		VALUES ($1, $2, $3, 1, 1, 4, 3000, 'JPY')`
	insertHeld := `INSERT INTO checkouts (id, status, hold_expire_at, order_id, ticket_sale_id)
		VALUES ($1, 1, now() + interval '15 minutes', $2, $3)`
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

	t.Run("an application started during the removal is refused", func(t *testing.T) {
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		orderID := entity.NewID()

		removal, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = removal.Rollback(ctx) }()
		require.NoError(t, rdb.LockAndCheckEventRemoval(ctx, removal, []string{o.eventID}))

		// The application stores its Pending Order between the check and the
		// removal, the window the stock row locks close.
		purchase, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = purchase.Rollback(ctx) }()
		pid := backendPID(t, purchase)
		inserted := make(chan error, 1)
		go func() {
			_, err := purchase.Exec(ctx, insertPending, orderID, saleFan(t), string(f.ID))
			inserted <- err
		}()
		require.Eventually(t, blocked(pid), 5*time.Second, 20*time.Millisecond, "the application waits for the removal")
		require.NoError(t, rdb.RemoveEvents(ctx, removal, []string{o.eventID}))
		require.NoError(t, removal.Commit(ctx))

		assert.True(t, rdb.IsForeignKeyViolation(<-inserted), "the application is refused on the removed ticket stock")
		assert.False(t, rowExists(t, `SELECT 1 FROM orders WHERE id = $1`, orderID), "no order")
		assert.False(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "the event is removed")
	})

	t.Run("a removal started during a checkout waits and refuses", func(t *testing.T) {
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		checkoutID, orderID := entity.NewID(), entity.NewID()

		purchase, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = purchase.Rollback(ctx) }()
		_, err = purchase.Exec(ctx, insertPending, orderID, saleFan(t), string(f.ID))
		require.NoError(t, err)
		_, err = purchase.Exec(ctx, insertHeld, checkoutID, orderID, string(f.SaleID))
		require.NoError(t, err)

		deleted := make(chan error, 1)
		go func() { deleted <- rdb.NewConcertRepository(testDB).DeleteAndSuppress(ctx, o.eventID) }()
		require.Eventually(t, backendBlocking(t, backendPID(t, purchase)), 5*time.Second, 20*time.Millisecond, "the removal waits for the checkout")
		require.NoError(t, purchase.Commit(ctx))

		assert.ErrorIs(t, <-deleted, apperr.ErrFailedPrecondition, "the held checkout blocks the removal")
		assert.True(t, rowExists(t, `SELECT 1 FROM checkouts WHERE id = $1`, checkoutID), "the checkout remains")
		assert.True(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "the event remains")
	})

	t.Run("a removal started during an order issuance waits without a deadlock and refuses", func(t *testing.T) {
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		fan := saleFan(t)
		captured := time.Now()
		checkoutID, orderID := insertStockCheckout(t, f, fan, entity.OrderStatusPending, entity.CheckoutStatusCommitted, 1, captured, true, nil)
		_, err := testDB.Pool.Exec(ctx, `UPDATE checkouts SET capture_at = now() WHERE id = $1`, checkoutID)
		require.NoError(t, err)

		// The issuance: complete the checkout (locking its row), then, once the
		// removal waits, make the Order Paid and insert its Ticket, whose
		// foreign keys reach the order and the event.
		issuance, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = issuance.Rollback(ctx) }()
		_, err = issuance.Exec(ctx, `UPDATE checkouts SET status = 3 WHERE id = $1`, checkoutID)
		require.NoError(t, err)

		deleted := make(chan error, 1)
		go func() { deleted <- rdb.NewConcertRepository(testDB).DeleteAndSuppress(ctx, o.eventID) }()
		require.Eventually(t, backendBlocking(t, backendPID(t, issuance)), 5*time.Second, 20*time.Millisecond, "the removal waits for the issuance")
		_, err = issuance.Exec(ctx, `UPDATE orders SET status = 1, provider_payment_id = 'pi_issue', pay_at = now() WHERE id = $1`, orderID)
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
