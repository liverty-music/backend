package migrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listingCancelledVersion is
// 20261106120000_add_resale_entry_lost_reason_listing_cancelled.sql.
const listingCancelledVersion = "20261106120000"

// TestMigration_ResaleEntryLostReasonListingCancelled checks that a Lost
// resale entry with the lost reason 5 ("listing cancelled") is rejected
// before 20261106120000_add_resale_entry_lost_reason_listing_cancelled.sql
// and stored after it, and that a reason above 5 is still rejected.
func TestMigration_ResaleEntryLostReasonListingCancelled(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(resaleEntriesVersion)

	var (
		venueID    = id(1)
		seriesID   = id(2)
		eventID    = id(3)
		seatTypeID = id(4)
		stockID    = id(5)
		fanID      = id(10)
		orderID    = id(20)
		otherOrder = id(21)
		entryID    = id(30)
	)
	now := time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO venues (id, name) VALUES ($1, 'Zepp Haneda')`, venueID)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour 2026', 'TOUR')`, seriesID)
	mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2026-12-01')`, eventID, seriesID, venueID)
	mustExec(`INSERT INTO seat_types (id, series_id, name, price) VALUES ($1, $2, 'S席', 5000)`, seatTypeID, seriesID)
	mustExec(`INSERT INTO ticket_stocks (id, event_id, seat_type_id, quantity) VALUES ($1, $2, $3, 10)`, stockID, eventID, seatTypeID)
	for _, o := range []string{orderID, otherOrder} {
		mustExec(`INSERT INTO orders (id, user_id, provider, status, amount, currency, ticket_stock_id, ticket_count)
			VALUES ($1, $2, 1, 5, 5000, 'JPY', $3, 1)`, o, fanID, stockID)
	}
	insertLost := func(entry, order string, reason int) error {
		_, err := conn.Exec(ctx, `INSERT INTO resale_entries (id, order_id, state, lost_reason, created_at, draw_at) VALUES ($1, $2, 3, $3, $4, $4)`,
			entry, order, reason, now)
		return err
	}

	require.Error(t, insertLost(entryID, orderID, 5), "the lost reason 5 is rejected before the migration")

	apply(listingCancelledVersion)

	require.NoError(t, insertLost(entryID, orderID, 5), "a Lost entry with the lost reason 5 is stored")
	var reason int
	require.NoError(t, conn.QueryRow(ctx, `SELECT lost_reason FROM resale_entries WHERE id = $1`, entryID).Scan(&reason))
	assert.Equal(t, 5, reason)
	assert.Error(t, insertLost(id(31), otherOrder, 6), "a lost reason above 5 is still rejected")
}
