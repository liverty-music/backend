package migrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// wallClockTimesPrevious is the last migration before
	// 20261126060405_wall_clock_times.sql.
	wallClockTimesPrevious = "20261126024811"
	// wallClockTimesVersion is 20261126060405_wall_clock_times.sql.
	wallClockTimesVersion = "20261126060405"
)

// TestMigration_WallClockTimes loads events at venues in several time zones,
// sales, candidates and a rejected concert with instants, applies
// 20261126060405_wall_clock_times.sql and checks that each is read back as the
// wall-clock time in its zone, cut to the minute, and that the new natural key
// and the sale convergence key hold.
func TestMigration_WallClockTimes(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(wallClockTimesPrevious)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO time_zones (name, name_en) VALUES ('Asia/Tokyo', 'Japan'), ('Asia/Seoul', 'Seoul'), ('Europe/London', 'London')
		ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO country_time_zones (country_code, time_zone) VALUES ('JP', 'Asia/Tokyo'), ('KR', 'Asia/Seoul'), ('GB', 'Europe/London')
		ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO areas (code, kind, country_code, name_en) VALUES ('JP-13', 'subdivision', 'JP', 'Tokyo'),
		('KR-11', 'subdivision', 'KR', 'Seoul'), ('GB-ENG', 'subdivision', 'GB', 'England') ON CONFLICT DO NOTHING`)

	tokyoVenue, seoulVenue, londonVenue := id(700), id(701), id(702)
	for _, v := range []struct{ id, name, area, zone string }{
		{tokyoVenue, "Zepp Haneda", "JP-13", "Asia/Tokyo"},
		{seoulVenue, "KSPO Dome", "KR-11", "Asia/Seoul"},
		{londonVenue, "The O2", "GB-ENG", "Europe/London"},
	} {
		mustExec(`INSERT INTO venues (id, name, admin_area, time_zone) VALUES ($1, $2, $3, $4)`, v.id, v.name, v.area, v.zone)
	}

	series := id(710)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour', 'TOUR')`, series)
	event := func(eventID, venueID, date string, start, open any) {
		mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date, start_time, open_time) VALUES ($1, $2, $3, $4, $5, $6)`,
			eventID, series, venueID, date, start, open)
	}
	// 19:00 JST at a Seoul venue is 10:00 UTC; the zones differ in name only
	// by their offsets, both UTC+9.
	seoulEvent, londonEvent, tokyoEvent, noStartEvent, secondsEvent := id(720), id(721), id(722), id(723), id(724)
	event(seoulEvent, seoulVenue, "2027-01-20", "2027-01-20T10:00:00Z", "2027-01-20T09:00:00Z")
	event(londonEvent, londonVenue, "2026-07-10", "2026-07-10T10:00:00Z", nil)
	event(tokyoEvent, tokyoVenue, "2026-12-01", "2026-12-01T09:00:00Z", "2026-12-01T08:00:00Z")
	event(noStartEvent, tokyoVenue, "2026-12-02", nil, nil)
	event(secondsEvent, tokyoVenue, "2026-12-03", "2026-12-03T09:00:30Z", nil)

	// An Organizer-less (discovered) sale of the series, found with seconds.
	saleDiscovered := id(730)
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, sale_start_time, sale_end_time, find_time)
		VALUES ($1, $2, 'Presale', 1, '2026-11-01T03:00:16.363Z', '2026-11-02T14:59:59Z', now())`, saleDiscovered, series)

	candidateArtist := id(740)
	mustExec(`INSERT INTO artists (id, name, mbid) VALUES ($1, 'Artist', '00000000-0000-0000-0000-000000000009')`, candidateArtist)
	candidateSeoul, candidateNoZone := id(741), id(742)
	candidateAbroad, candidateNextDay := id(744), id(745)
	mustExec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, local_date, start_time, open_time, listed_venue_name, time_zone)
		VALUES ($1, $2, $3, 'T', '2027-01-20', '2027-01-20T10:00:00Z', NULL, 'KSPO Dome', 'Asia/Seoul')`, candidateSeoul, candidateArtist, series)
	mustExec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, local_date, start_time, open_time, listed_venue_name)
		VALUES ($1, $2, $3, 'T', '2027-01-21', '2027-01-21T09:00:00Z', NULL, 'Hall')`, candidateNoZone, candidateArtist, series)
	// A candidate abroad without a zone, and one in Japan whose instant falls
	// on the day after its date: their times cannot be told.
	mustExec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, local_date, start_time, listed_venue_name, admin_area)
		VALUES ($1, $2, $3, 'T', '2027-01-22', '2027-01-22T09:00:00Z', 'Legacy Taipei', 'TW-TPE')`, candidateAbroad, candidateArtist, series)
	mustExec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, local_date, start_time, listed_venue_name, admin_area)
		VALUES ($1, $2, $3, 'T', '2027-01-23', '2027-01-23T18:00:00Z', 'Hall', 'JP-13')`, candidateNextDay, candidateArtist, series)
	rejected := id(743)
	mustExec(`INSERT INTO rejected_concerts (id, artist_id, artist_name, title, local_date, start_time, listed_venue_name, time_zone, reason)
		VALUES ($1, $2, 'Artist', 'T', '2027-01-20', '2027-01-20T10:00:00Z', 'KSPO Dome', 'Asia/Seoul', 'no')`, rejected, candidateArtist)

	apply(wallClockTimesVersion)

	localTimes := func(eventID string) (start, open *string) {
		t.Helper()
		require.NoError(t, conn.QueryRow(ctx, `SELECT local_start_time::text, local_open_time::text FROM events WHERE id = $1`, eventID).Scan(&start, &open))
		return
	}

	t.Run("a Seoul venue reads 19:00 from a 19:00 JST instant", func(t *testing.T) {
		// @spec components/entity/event "Show in Seoul"
		start, open := localTimes(seoulEvent)
		require.NotNil(t, start)
		assert.Equal(t, "19:00:00", *start)
		require.NotNil(t, open)
		assert.Equal(t, "18:00:00", *open)
	})
	t.Run("a London venue in July reads 11:00 from a 10:00 UTC instant", func(t *testing.T) {
		// @spec components/entity/event "Show in London"
		start, open := localTimes(londonEvent)
		require.NotNil(t, start)
		assert.Equal(t, "11:00:00", *start)
		assert.Nil(t, open)
	})
	t.Run("a Tokyo venue keeps its Japan time", func(t *testing.T) {
		start, open := localTimes(tokyoEvent)
		assert.Equal(t, "18:00:00", *start)
		assert.Equal(t, "17:00:00", *open)
	})
	t.Run("an unknown start stays unknown", func(t *testing.T) {
		start, open := localTimes(noStartEvent)
		assert.Nil(t, start)
		assert.Nil(t, open)
	})
	t.Run("seconds are cut", func(t *testing.T) {
		start, _ := localTimes(secondsEvent)
		assert.Equal(t, "18:00:00", *start)
	})
	t.Run("a discovered sale takes the zone its series' venues share, else Japan time, and is cut to the minute", func(t *testing.T) {
		var zone, start, end string
		require.NoError(t, conn.QueryRow(ctx, `SELECT time_zone, local_start_time::text, local_end_time::text FROM ticket_sales WHERE id = $1`, saleDiscovered).
			Scan(&zone, &start, &end))
		assert.Equal(t, "Asia/Tokyo", zone, "the series' venues span three zones")
		assert.Equal(t, "2026-11-01 12:00:00", start)
		assert.Equal(t, "2026-11-02 23:59:00", end)
	})
	t.Run("a candidate is read in its own zone, Japan time without one", func(t *testing.T) {
		var seoul, none *string
		require.NoError(t, conn.QueryRow(ctx, `SELECT local_start_time::text FROM concert_candidates WHERE id = $1`, candidateSeoul).Scan(&seoul))
		require.NoError(t, conn.QueryRow(ctx, `SELECT local_start_time::text FROM concert_candidates WHERE id = $1`, candidateNoZone).Scan(&none))
		assert.Equal(t, "19:00:00", *seoul)
		assert.Equal(t, "18:00:00", *none)
		var rejectedStart *string
		require.NoError(t, conn.QueryRow(ctx, `SELECT local_start_time::text FROM rejected_concerts WHERE id = $1`, rejected).Scan(&rejectedStart))
		assert.Equal(t, "19:00:00", *rejectedStart)
	})
	t.Run("a candidate time that cannot be told stays unknown", func(t *testing.T) {
		for _, candidate := range []string{candidateAbroad, candidateNextDay} {
			var start *string
			require.NoError(t, conn.QueryRow(ctx, `SELECT local_start_time::text FROM concert_candidates WHERE id = $1`, candidate).Scan(&start))
			assert.Nil(t, start)
		}
	})
	t.Run("the natural key is the venue, date and local start time", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO events (id, series_id, venue_id, local_event_date, local_start_time) VALUES ($1, $2, $3, '2026-12-01', '18:00')`,
			id(790), series, tokyoVenue)
		assert.Error(t, err, "the same venue, date and start")
		_, err = conn.Exec(ctx, `INSERT INTO events (id, series_id, venue_id, local_event_date, local_start_time) VALUES ($1, $2, $3, '2026-12-01', '21:00')`,
			id(791), series, tokyoVenue)
		assert.NoError(t, err, "another start the same day")
	})
	t.Run("a sale's window is whole minutes in a known zone", func(t *testing.T) {
		_, err := conn.Exec(ctx, `UPDATE ticket_sales SET local_end_time = local_end_time + interval '30 seconds' WHERE id = $1`, saleDiscovered)
		assert.Error(t, err)
		_, err = conn.Exec(ctx, `UPDATE ticket_sales SET time_zone = 'Mars/Olympus' WHERE id = $1`, saleDiscovered)
		assert.Error(t, err)
	})
	t.Run("two discovered sales of one series and method start on one local date once", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO ticket_sales (id, series_id, name, method, time_zone, local_start_time, local_end_time, find_time)
			VALUES ($1, $2, 'Again', 1, 'Asia/Tokyo', '2026-11-01 20:00', '2026-11-03 00:00', now())`, id(792), series)
		assert.Error(t, err)
	})

}
