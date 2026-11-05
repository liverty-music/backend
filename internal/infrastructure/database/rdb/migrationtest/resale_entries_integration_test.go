package migrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resaleEntriesPrevious is the last migration before
// 20261105130000_add_resale_entries_and_card_authorizations.sql.
const resaleEntriesPrevious = "20261103101331"

// resaleEntriesVersion is
// 20261105130000_add_resale_entries_and_card_authorizations.sql.
const resaleEntriesVersion = "20261105130000"

// TestMigration_ResaleEntriesAndCardAuthorizations loads a Paid order and a
// Void order that name the payment provider's ID of their card authorization
// and a Pending order without one, applies
// 20261105130000_add_resale_entries_and_card_authorizations.sql and checks
// that each named card authorization got a record with a version-7 id that
// its order now names, that the order without one still names none, that the
// resale entry checks reject a lost reason outside the Lost state, and that
// delete_ticket_stock removes a Void order with its resale entry and card
// authorization.
func TestMigration_ResaleEntriesAndCardAuthorizations(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(resaleEntriesPrevious)

	var (
		venueID      = id(1)
		seriesID     = id(2)
		eventID      = id(3)
		seatTypeID   = id(4)
		stockID      = id(5)
		voidStockID  = id(6)
		fanID        = id(10)
		paidOrderID  = id(20)
		voidOrderID  = id(21)
		pendingOrder = id(22)
	)
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

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
	mustExec(`INSERT INTO seat_types (id, series_id, name, price) VALUES ($1, $2, 'A席', 3000)`, id(7), seriesID)
	mustExec(`INSERT INTO ticket_stocks (id, event_id, seat_type_id, quantity) VALUES ($1, $2, $3, 10)`, voidStockID, eventID, id(7))
	mustExec(`INSERT INTO orders (id, user_id, provider, provider_payment_id, status, amount, currency, pay_at, ticket_stock_id, ticket_count, card_authorization_id)
		VALUES ($1, $2, 1, 'pi_paid', 1, 10000, 'JPY', $3, $4, 2, 'pi_paid')`, paidOrderID, fanID, now, stockID)
	mustExec(`INSERT INTO orders (id, user_id, provider, status, amount, currency, ticket_stock_id, ticket_count, card_authorization_id)
		VALUES ($1, $2, 1, 5, 3000, 'JPY', $3, 1, 'pi_void')`, voidOrderID, fanID, voidStockID)
	mustExec(`INSERT INTO orders (id, user_id, provider, status, amount, currency, ticket_stock_id, ticket_count)
		VALUES ($1, $2, 1, 4, 5000, 'JPY', $3, 1)`, pendingOrder, fanID, stockID)

	apply(resaleEntriesVersion)

	t.Run("each named card authorization has a record its order names", func(t *testing.T) {
		for orderID, providerID := range map[string]string{paidOrderID: "pi_paid", voidOrderID: "pi_void"} {
			var caID, version string
			require.NoError(t, conn.QueryRow(ctx, `
				SELECT ca.id::text, substring(ca.id::text, 15, 1)
				FROM orders o JOIN card_authorizations ca ON ca.id = o.card_authorization_id
				WHERE o.id = $1 AND ca.provider_card_authorization_id = $2`, orderID, providerID).Scan(&caID, &version))
			assert.Equal(t, "7", version, "the record's id is marked version 7")
		}
		var count int
		require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM card_authorizations`).Scan(&count))
		assert.Equal(t, 2, count)

		var named bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT card_authorization_id IS NOT NULL FROM orders WHERE id = $1`, pendingOrder).Scan(&named))
		assert.False(t, named, "an order that named no card authorization still names none")
	})

	t.Run("a lost reason outside the Lost state is rejected", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO resale_entries (id, order_id, state, lost_reason, created_at) VALUES ($1, $2, 1, 1, $3)`,
			id(30), pendingOrder, now)
		assert.Error(t, err)
	})

	t.Run("deleting a stock removes its Void order's resale entry and card authorization", func(t *testing.T) {
		mustExec(`INSERT INTO resale_entries (id, order_id, state, created_at, withdraw_at) VALUES ($1, $2, 5, $3, $3)`, id(31), voidOrderID, now)

		var deleted bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT delete_ticket_stock($1)`, voidStockID).Scan(&deleted))
		assert.True(t, deleted)

		var entries, cards int
		require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM resale_entries`).Scan(&entries))
		require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM card_authorizations WHERE provider_card_authorization_id = 'pi_void'`).Scan(&cards))
		assert.Zero(t, entries)
		assert.Zero(t, cards)
	})
}
