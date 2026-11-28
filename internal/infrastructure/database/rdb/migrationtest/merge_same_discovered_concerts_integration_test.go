package migrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// mergeSameConcertsPrevious is the last migration before
	// 20261128100814_merge_same_discovered_concerts.sql.
	mergeSameConcertsPrevious = "20261128071639"
	// mergeSameConcertsVersion is
	// 20261128100814_merge_same_discovered_concerts.sql.
	mergeSameConcertsVersion = "20261128100814"
)

// Ids the migration lists: one duplicate pair (Lucky Kilimanjaro, 2027-03-13)
// and one split tour (Mrs. GREEN APPLE). The migration's other ids are absent
// here, which also checks that it skips rows that no longer exist.
const (
	pairKeep        = "01a05c4c-c490-7907-afc6-c307fe8af438"
	pairDrop        = "01a1966b-31cc-761e-aadc-a5590b760ff3"
	tourCanonical   = "019f8cd8-774e-77ea-9047-973899a4b6a6"
	tourMember      = "01a21c3d-7ead-7162-b350-1251f02bc077"
	saleKept        = "019a0000-0101-7000-8000-000000000000"
	saleSameDay     = "019a0000-0102-7000-8000-000000000000"
	saleOtherMethod = "019a0000-0103-7000-8000-000000000000"
)

// TestMigration_MergeSameDiscoveredConcerts loads a duplicate pair (the newer
// event has the times and a fan's ticket journey) and a tour split over two
// Series (a discovered ticket sale on each, the member's on the same method
// and start date as the canonical one's and with a reminder log row, plus a
// second sale on the member), applies
// 20261128100814_merge_same_discovered_concerts.sql, and checks one event with
// both names, the times and the journey, one Series, and one sale per method
// and start date.
func TestMigration_MergeSameDiscoveredConcerts(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(mergeSameConcertsPrevious)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO time_zones (name, name_en) VALUES ('Asia/Tokyo', 'Japan') ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO country_time_zones (country_code, time_zone) VALUES ('JP', 'Asia/Tokyo') ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO areas (code, kind, country_code, name_en) VALUES ('JP-04', 'subdivision', 'JP', 'Miyagi') ON CONFLICT DO NOTHING`)
	artist, venue, pairSeries, tourEvent1, tourEvent2, fan := id(1), id(2), id(3), id(4), id(5), id(6)
	mustExec(`INSERT INTO artists (id, name, mbid) VALUES ($1, 'Artist', '00000000-0000-0000-0000-000000000001')`, artist)
	mustExec(`INSERT INTO venues (id, name, admin_area, time_zone) VALUES ($1, 'SENDAI PIT', 'JP-04', 'Asia/Tokyo')`, venue)
	mustExec(`INSERT INTO users (id, provider_user_id, name, email, preferred_language) VALUES ($1, 'fan', 'Fan', 'fan@example.com', 'ja')`, fan)

	// The duplicate pair: the older event has no times; the newer one has them
	// and a fan's ticket journey.
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Pair tour', 'TOUR')`, pairSeries)
	mustExec(`INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date) VALUES ($1, $2, $3, 'Sendai PIT', '2027-03-13')`,
		pairKeep, pairSeries, venue)
	mustExec(`INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date, local_open_time, local_start_time)
		VALUES ($1, $2, $3, 'SENDAI PIT', '2027-03-13', '17:00', '18:00')`, pairDrop, pairSeries, venue)
	for _, e := range []string{pairKeep, pairDrop} {
		mustExec(`INSERT INTO concerts (event_id) VALUES ($1)`, e)
		mustExec(`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`, e, artist)
	}
	mustExec(`INSERT INTO ticket_journeys (user_id, event_id, status) VALUES ($1, $2, 2)`, fan, pairDrop)

	// The split tour: one event on each Series.
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Ringo Jam Tour', 'TOUR'), ($2, 'Ringo Jam Tour', 'TOUR')`, tourCanonical, tourMember)
	mustExec(`INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date) VALUES ($1, $2, $3, 'SENDAI PIT', '2027-05-01'), ($4, $5, $3, 'SENDAI PIT', '2027-05-02')`,
		tourEvent1, tourCanonical, venue, tourEvent2, tourMember)
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, time_zone, local_start_time, local_end_time, find_time) VALUES
		($1, $2, 'FC lottery', 2, 'Asia/Tokyo', '2027-01-10 12:00', '2027-01-20 23:59', '2026-11-01 00:00+00'),
		($3, $4, 'FC lottery', 2, 'Asia/Tokyo', '2027-01-10 18:00', '2027-01-21 23:59', '2026-11-27 00:00+00'),
		($5, $4, 'General', 1, 'Asia/Tokyo', '2027-02-01 10:00', '2027-02-10 23:59', '2026-11-27 00:00+00')`,
		saleKept, tourCanonical, saleSameDay, tourMember, saleOtherMethod)
	mustExec(`INSERT INTO ticket_sale_events (ticket_sale_id, event_id) VALUES ($1, $2), ($3, $4), ($5, $4)`,
		saleKept, tourEvent1, saleSameDay, tourEvent2, saleOtherMethod)
	mustExec(`INSERT INTO ticket_sale_reminders (id, user_id, ticket_sale_id, stage) VALUES ($1, $2, $3, 1)`, id(7), fan, saleSameDay)
	mustExec(`INSERT INTO ticket_sale_searches (series_id, search_time) VALUES ($1, '2026-11-20 00:00+00')`, tourMember)

	apply(mergeSameConcertsVersion)

	// One event with both names, the newer one's times, and the journey.
	var (
		listed      string
		others      []string
		open, start string
		journeys    int
	)
	require.NoError(t, conn.QueryRow(ctx, `SELECT listed_venue_name, other_listed_venue_names, local_open_time::text, local_start_time::text FROM events WHERE id = $1`, pairKeep).
		Scan(&listed, &others, &open, &start))
	assert.Equal(t, "SENDAI PIT", listed)
	assert.Equal(t, []string{"Sendai PIT"}, others)
	assert.Equal(t, "17:00:00", open)
	assert.Equal(t, "18:00:00", start)
	var dropLeft int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM events WHERE id = $1`, pairDrop).Scan(&dropLeft))
	assert.Zero(t, dropLeft, "the newer event of the pair is deleted")
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM ticket_journeys WHERE user_id = $1 AND event_id = $2`, fan, pairKeep).Scan(&journeys))
	assert.Equal(t, 1, journeys, "the fan's ticket journey moved to the kept event")

	// One Series, holding both events and one sale per method and start date.
	var seriesLeft int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM series WHERE id = $1`, tourMember).Scan(&seriesLeft))
	assert.Zero(t, seriesLeft, "the member Series is deleted")
	var tourEvents int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM events WHERE series_id = $1`, tourCanonical).Scan(&tourEvents))
	assert.Equal(t, 2, tourEvents)

	rows, err := conn.Query(ctx, `SELECT id::text FROM ticket_sales WHERE series_id = $1 ORDER BY id`, tourCanonical)
	require.NoError(t, err)
	var sales []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		sales = append(sales, s)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{saleKept, saleOtherMethod}, sales, "the later-found sale on the same method and start date is dropped")

	var reminders, keptEvents int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM ticket_sale_reminders WHERE user_id = $1 AND ticket_sale_id = $2 AND stage = 1`, fan, saleKept).Scan(&reminders))
	assert.Equal(t, 1, reminders, "the kept sale takes over the reminder log")
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM ticket_sale_events WHERE ticket_sale_id = $1`, saleKept).Scan(&keptEvents))
	assert.Equal(t, 2, keptEvents, "the kept sale takes over the dropped sale's event")

	var searched int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM ticket_sale_searches WHERE series_id = $1 AND search_time = '2026-11-20 00:00+00'`, tourCanonical).Scan(&searched))
	assert.Equal(t, 1, searched, "the canonical Series keeps the group's oldest ticket sale search")

	var backedUp int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM _merge_same_concerts_backup WHERE entity IN ('events deleted', 'series deleted', 'ticket_sales deleted')`).Scan(&backedUp))
	assert.Equal(t, 3, backedUp, "every deleted event, Series and sale is saved for rollback")
}
