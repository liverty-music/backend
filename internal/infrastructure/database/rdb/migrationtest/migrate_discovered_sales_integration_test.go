package migrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// migrateDiscoveredSalesPrevious is the last migration before
	// 20261013002947_migrate_discovered_sales.sql.
	migrateDiscoveredSalesPrevious = "20261013000100"

	// migrateDiscoveredSalesVersion is 20261013002947_migrate_discovered_sales.sql.
	migrateDiscoveredSalesVersion = "20261013002947"
)

// TestMigration_MigrateDiscoveredSales loads four `sales_phases` rows (a
// lottery with an end and a result time, a lottery without a result time, a
// first-come sale without an end and one with an end), two reminder records
// (stages 1 and 4), one search log, one `sales_phase_announcement`
// notification and one Organizer's lottery sale; it then applies
// 20261013002947_migrate_discovered_sales.sql and checks every row.
func TestMigration_MigrateDiscoveredSales(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(migrateDiscoveredSalesPrevious)

	var (
		venueID         = id(1)
		discSeries      = id(2) // discovered series, no organizer
		orgSeries       = id(3) // an Organizer's series
		organizer       = id(4)
		orgEvent        = id(5)
		userID          = id(10)
		lotteryFull     = id(20) // lottery with an end and a result time
		lotteryNoResult = id(21) // lottery without a result time
		firstComeOpen   = id(22) // first-come sale without an end
		firstComeEnd    = id(23) // first-come sale with an end
		reminderOpen    = id(30)
		reminderResult  = id(31)
		notificationID  = id(40)
		orgSale         = id(50)
		orgTicketType   = id(51)
	)
	jst := time.FixedZone("JST", 9*60*60)
	at := func(month time.Month, day, hour int) time.Time {
		return time.Date(2026, month, day, hour, 0, 0, 0, jst)
	}
	discovered := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO venues (id, name) VALUES ($1, 'Zepp Haneda')`, venueID)
	mustExec(`INSERT INTO organizers (id, name, operator_email, status) VALUES ($1, 'Label', 'op@example.com', 2)`, organizer)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Discovered Tour', 'TOUR')`, discSeries)
	mustExec(`INSERT INTO series (id, title, type, organizer_id, visibility) VALUES ($1, 'Organizer Tour', 'TOUR', $2, 'PUBLIC')`,
		orgSeries, organizer)
	mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state) VALUES ($1, $2, $3, '2026-12-01', 'PUBLISHED')`,
		orgEvent, orgSeries, venueID)
	mustExec(`INSERT INTO users (id, provider_user_id, name, email) VALUES ($1, 'ext-1', 'Fan', 'fan@example.com')`, userID)

	// sales_phases.method: 1=LOTTERY, 2=FIRST_COME.
	mustExec(`INSERT INTO sales_phases (id, series_id, method, apply_start_at, apply_end_at, lottery_result_at, discovered_at)
	      VALUES ($1, $2, 1, $3, $4, $5, $6)`, lotteryFull, discSeries, at(10, 5, 18), at(10, 22, 23), at(11, 3, 15), discovered)
	mustExec(`INSERT INTO sales_phases (id, series_id, method, apply_start_at, apply_end_at, discovered_at)
	      VALUES ($1, $2, 1, $3, $4, $5)`, lotteryNoResult, discSeries, at(11, 10, 12), at(11, 20, 23), discovered)
	mustExec(`INSERT INTO sales_phases (id, series_id, method, apply_start_at, discovered_at)
	      VALUES ($1, $2, 2, $3, $4)`, firstComeOpen, discSeries, at(10, 6, 19), discovered)
	mustExec(`INSERT INTO sales_phases (id, series_id, method, apply_start_at, apply_end_at, discovered_at)
	      VALUES ($1, $2, 2, $3, $4, $5)`, firstComeEnd, discSeries, at(12, 1, 10), at(12, 3, 18), discovered)

	mustExec(`INSERT INTO sales_phase_reminders (id, user_id, sales_phase_id, stage) VALUES ($1, $2, $3, 1), ($4, $2, $3, 4)`,
		reminderOpen, userID, lotteryFull, reminderResult)
	mustExec(`INSERT INTO sales_phase_search_logs (series_id, searched_at) VALUES ($1, $2)`, discSeries, discovered)
	mustExec(`INSERT INTO notifications (id, user_id, type, payload) VALUES ($1, $2, 'sales_phase_announcement', '{}')`,
		notificationID, userID)

	// An Organizer's lottery sale as unify-ticket-sales left it (method 2 = Lottery).
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at, verification_requirement)
	      VALUES ($1, $2, '抽選', 2, $3, $4, 2)`, orgSale, orgSeries, at(10, 5, 18), at(10, 12, 23))
	mustExec(`INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit)
	      VALUES ($1, $2, $3, 8000, 100, 4)`, orgTicketType, orgSale, orgEvent)

	apply(migrateDiscoveredSalesVersion)

	type saleRow struct {
		seriesID     string
		name         *string
		method       int16
		start        time.Time
		end          *time.Time
		result       *time.Time
		discoveredAt *time.Time
		verification int16
		drawAt       *time.Time
		startDateJST time.Time
	}
	readSale := func(saleID string) saleRow {
		t.Helper()
		var r saleRow
		require.NoError(t, conn.QueryRow(ctx, `
			SELECT series_id, name, method, sale_start_at, sale_end_at, lottery_result_at, discovered_at,
			       verification_requirement, draw_at, sale_start_date_jst
			FROM ticket_sales WHERE id = $1`, saleID).Scan(
			&r.seriesID, &r.name, &r.method, &r.start, &r.end, &r.result, &r.discoveredAt,
			&r.verification, &r.drawAt, &r.startDateJST))
		return r
	}
	timeEq := func(t *testing.T, want time.Time, got *time.Time) {
		t.Helper()
		require.NotNil(t, got)
		assert.True(t, want.Equal(*got), "want %s, got %s", want, got)
	}

	t.Run("each sales phase becomes a discovered sale with its id", func(t *testing.T) {
		full := readSale(lotteryFull)
		assert.Equal(t, discSeries, full.seriesID)
		assert.Nil(t, full.name)
		assert.Equal(t, int16(2), full.method, "LOTTERY maps to Lottery = 2")
		assert.True(t, at(10, 5, 18).Equal(full.start))
		timeEq(t, at(10, 22, 23), full.end)
		timeEq(t, at(11, 3, 15), full.result)
		timeEq(t, discovered, full.discoveredAt)
		assert.Equal(t, int16(0), full.verification)
		assert.Nil(t, full.drawAt)
		assert.Equal(t, "2026-10-05", full.startDateJST.Format(time.DateOnly))

		noResult := readSale(lotteryNoResult)
		assert.Equal(t, int16(2), noResult.method)
		timeEq(t, at(11, 20, 23), noResult.end)
		assert.Nil(t, noResult.result)

		open := readSale(firstComeOpen)
		assert.Equal(t, int16(1), open.method, "FIRST_COME maps to FirstCome = 1")
		assert.Nil(t, open.end)
		assert.Nil(t, open.result)

		ended := readSale(firstComeEnd)
		assert.Equal(t, int16(1), ended.method)
		timeEq(t, at(12, 3, 18), ended.end)

		var ticketTypes int
		require.NoError(t, conn.QueryRow(ctx,
			`SELECT count(*) FROM ticket_types WHERE ticket_sale_id = ANY($1::uuid[])`,
			[]string{lotteryFull, lotteryNoResult, firstComeOpen, firstComeEnd}).Scan(&ticketTypes))
		assert.Zero(t, ticketTypes, "a discovered sale offers no ticket type")
	})

	t.Run("the reminders point at the same sales", func(t *testing.T) {
		rows, err := conn.Query(ctx, `SELECT id, user_id, ticket_sale_id, stage FROM ticket_sale_reminders ORDER BY id`)
		require.NoError(t, err)
		type reminder struct {
			id, userID, saleID string
			stage              int16
		}
		var got []reminder
		for rows.Next() {
			var r reminder
			require.NoError(t, rows.Scan(&r.id, &r.userID, &r.saleID, &r.stage))
			got = append(got, r)
		}
		require.NoError(t, rows.Err())
		assert.Equal(t, []reminder{
			{id: reminderOpen, userID: userID, saleID: lotteryFull, stage: 1},
			{id: reminderResult, userID: userID, saleID: lotteryFull, stage: 4},
		}, got)
	})

	t.Run("the search log moves", func(t *testing.T) {
		var searched time.Time
		require.NoError(t, conn.QueryRow(ctx, `SELECT searched_at FROM ticket_sale_searches WHERE series_id = $1`, discSeries).Scan(&searched))
		assert.True(t, discovered.Equal(searched))
	})

	t.Run("the announcement notification type is renamed", func(t *testing.T) {
		var typ string
		require.NoError(t, conn.QueryRow(ctx, `SELECT type FROM notifications WHERE id = $1`, notificationID).Scan(&typ))
		assert.Equal(t, "ticket_sale_announcement", typ)
	})

	t.Run("the Organizer's sale is untouched", func(t *testing.T) {
		org := readSale(orgSale)
		require.NotNil(t, org.name)
		assert.Equal(t, "抽選", *org.name)
		assert.Equal(t, int16(2), org.method)
		timeEq(t, at(10, 12, 23), org.end)
		assert.Nil(t, org.result)
		assert.Nil(t, org.discoveredAt)
		assert.Equal(t, int16(2), org.verification)
	})

	t.Run("the old tables are gone", func(t *testing.T) {
		var n int
		require.NoError(t, conn.QueryRow(ctx,
			`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'app' AND table_name LIKE 'sales_phase%'`).Scan(&n))
		assert.Zero(t, n)
	})

	t.Run("a second discovered sale on the same key is refused", func(t *testing.T) {
		_, err := conn.Exec(ctx, `
			INSERT INTO ticket_sales (id, series_id, method, sale_start_at, sale_end_at, discovered_at)
			VALUES ($1, $2, 2, $3, $4, now())`, id(60), discSeries, at(10, 5, 9), at(10, 9, 23))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uq_ticket_sales_discovered_convergence")
	})

	t.Run("a second Organizer's sale of the series on the same date is accepted", func(t *testing.T) {
		_, err := conn.Exec(ctx, `
			INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at)
			VALUES ($1, $2, 'FC先行', 2, $3, $4)`, id(61), orgSeries, at(10, 5, 20), at(10, 10, 23))
		require.NoError(t, err)
	})

	t.Run("the kind CHECKs hold", func(t *testing.T) {
		for name, sql := range map[string]string{
			"discovered sale with a name": `INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at, discovered_at)
				VALUES ('` + id(70) + `', '` + discSeries + `', 'x', 1, now(), now() + interval '1 day', now())`,
			"Organizer's sale without a name": `INSERT INTO ticket_sales (id, series_id, method, sale_start_at, sale_end_at)
				VALUES ('` + id(71) + `', '` + orgSeries + `', 1, now(), now() + interval '1 day')`,
			"Organizer's first-come sale without an end": `INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at)
				VALUES ('` + id(72) + `', '` + orgSeries + `', 'x', 1, now())`,
			"discovered lottery without an end": `INSERT INTO ticket_sales (id, series_id, method, sale_start_at, discovered_at)
				VALUES ('` + id(73) + `', '` + discSeries + `', 2, now() + interval '30 day', now())`,
			"result time on an Organizer's lottery": `INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at, lottery_result_at)
				VALUES ('` + id(74) + `', '` + orgSeries + `', 'x', 2, now(), now() + interval '2 day', now() + interval '3 day')`,
			"result before the end": `INSERT INTO ticket_sales (id, series_id, method, sale_start_at, sale_end_at, lottery_result_at, discovered_at)
				VALUES ('` + id(75) + `', '` + discSeries + `', 2, now() + interval '40 day', now() + interval '42 day', now() + interval '41 day', now())`,
			"discovered sale requiring verification": `INSERT INTO ticket_sales (id, series_id, method, sale_start_at, verification_requirement, discovered_at)
				VALUES ('` + id(76) + `', '` + discSeries + `', 1, now() + interval '50 day', 1, now())`,
		} {
			_, err := conn.Exec(ctx, sql)
			assert.Error(t, err, name)
		}
	})
}
