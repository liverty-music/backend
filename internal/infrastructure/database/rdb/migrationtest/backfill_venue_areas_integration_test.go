package migrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// backfillVenueAreasPrevious is the last migration before
	// 20261126010000_backfill_venue_areas.sql.
	backfillVenueAreasPrevious = "20261125120000"
	// backfillVenueAreasVersion is 20261126010000_backfill_venue_areas.sql.
	backfillVenueAreasVersion = "20261126010000"
)

// Venue and Series ids the migration names.
const (
	privateVenue      = "01a05c53-642d-70cb-8696-0154903cee2b" // deleted: no place a fan can go to
	privateEvent      = "01a05c53-6432-777d-a2b3-b7441407f5a9"
	privateSeries     = "01a05c53-6338-7249-bd57-3bf8ca9b8c46"
	milkBoyDuplicate  = "01a05c51-2089-7c71-917b-e49f9363c708" // merged into milkBoy
	milkBoy           = "01a14e3d-e07f-7a54-9349-5a3f44708702"
	milkBoyEvent      = "01a05c51-2095-7d16-be60-9bb705e6d1b4"
	milkBoySeries     = "01a05c51-1dda-7a82-8cd4-3a0cdfe902c8"
	capitolTheatre    = "019e3939-1296-7e42-8b05-0e7856b20248" // reviewed area, wrong place
	taipeiArena       = "019e3432-7d64-76ee-8b4b-89a41e5b4652" // reviewed area
	singaporeIndoor   = "01a15364-9f67-70a7-a514-5b8582020c94" // renamed
	unusedUnannounced = "019e782c-766f-76b5-a996-276bc8ee579e" // deleted: no Event
	// taipeiArenaAgain is a row discovery stored for Taipei Arena after the
	// review; no migration names it.
	taipeiArenaAgain = "01a21600-0000-7000-8000-000000000001"
	taipeiEvent      = "01a21600-0000-7000-8000-000000000002"
	taipeiSeries     = "01a21600-0000-7000-8000-000000000003"
	candidateID      = "01a21600-0000-7000-8000-000000000004"
	// A second candidate of the same artist, date and name without a place.
	unresolvedCandidateID = "01a21600-0000-7000-8000-000000000005"
)

// TestMigration_BackfillVenueAreas applies
// 20261126010000_backfill_venue_areas.sql over one venue of each kind it
// changes and checks the deletions, the merge, the cleared places, the rename
// and the reviewed areas.
func TestMigration_BackfillVenueAreas(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(backfillVenueAreasPrevious)
	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO time_zones (name, name_en) VALUES ('Asia/Taipei', 'Taipei'), ('Asia/Singapore', 'Singapore'),
		('America/New_York', 'New York') ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO country_time_zones (country_code, time_zone) VALUES ('TW', 'Asia/Taipei'), ('SG', 'Asia/Singapore'),
		('US', 'America/New_York') ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO areas (code, kind, country_code, name_en) VALUES ('TW-TPE', 'subdivision', 'TW', 'Taipei'),
		('SG-01', 'subdivision', 'SG', 'Central Singapore'), ('US-PA', 'subdivision', 'US', 'Pennsylvania') ON CONFLICT DO NOTHING`)

	venue := func(venueID, listed string, area, zone *string, place string) {
		var placeID *string
		var lat, lng *float64
		if place != "" {
			placeID = &place
			la, ln := 35.0, 139.0
			lat, lng = &la, &ln
		}
		mustExec(`INSERT INTO venues (id, name, listed_venue_name, admin_area, time_zone, google_place_id, latitude, longitude, coordinates_fetch_time)
			VALUES ($1, $2, $2, $3, $4, $5, $6, $7, CASE WHEN $6::double precision IS NULL THEN NULL ELSE now() END)`,
			venueID, listed, area, zone, placeID, lat, lng)
	}
	pa, ny := "US-PA", "America/New_York"
	venue(privateVenue, "PRIVATE", nil, nil, "")
	venue(milkBoyDuplicate, "MilkBoy", nil, nil, "p-milkboy-japan")
	venue(milkBoy, "MilkBoy", &pa, &ny, "p-milkboy")
	venue(capitolTheatre, "Capitol Theatre", nil, nil, "p-capitol-sydney")
	venue(taipeiArena, "Taipei Arena", nil, nil, "p-taipei")
	venue(singaporeIndoor, "SINGAPORE INDOOR", nil, nil, "p-indoor")
	venue(unusedUnannounced, "上海公演 -STAY TUNED-", nil, nil, "")
	show := func(eventID, seriesID, venueID, date string) {
		mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour', 'SINGLE')`, seriesID)
		mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, $4)`, eventID, seriesID, venueID, date)
	}
	show(privateEvent, privateSeries, privateVenue, "2026-10-23")
	show(milkBoyEvent, milkBoySeries, milkBoyDuplicate, "2026-09-22")
	taipei, taipeiZone := "TW-TPE", "Asia/Taipei"
	venue(taipeiArenaAgain, "Taipei Arena", &taipei, &taipeiZone, "p-taipei-again")
	show(taipeiEvent, taipeiSeries, taipeiArena, "2026-08-29")
	mustExec(`INSERT INTO artists (id, name, mbid) VALUES ($1, 'Artist', '00000000-0000-0000-0000-000000000001')`, id(900))
	mustExec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, local_date, listed_venue_name, resolved_place_id)
		VALUES ($1, $2, $3, 'Tour', '2026-12-01', 'Capitol Theatre', 'p-capitol-sydney')`, candidateID, id(900), taipeiSeries)

	// A candidate that lost its place and one without a place share a name and date.
	mustExec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, local_date, listed_venue_name)
		VALUES ($1, $2, $3, 'Tour', '2026-12-01', 'Capitol Theatre')`, unresolvedCandidateID, id(900), taipeiSeries)

	apply(backfillVenueAreasVersion)

	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		require.NoError(t, conn.QueryRow(ctx, sql, args...).Scan(&n))
		return n
	}
	read := func(venueID string) (listed string, area, zone, place *string) {
		t.Helper()
		require.NoError(t, conn.QueryRow(ctx, `SELECT listed_venue_name, admin_area, time_zone, google_place_id FROM venues WHERE id = $1`, venueID).
			Scan(&listed, &area, &zone, &place))
		return
	}

	assert.Zero(t, count(`SELECT count(*) FROM venues WHERE id IN ($1, $2, $3)`, privateVenue, milkBoyDuplicate, unusedUnannounced))
	assert.Zero(t, count(`SELECT count(*) FROM events WHERE id = $1`, privateEvent))
	assert.Zero(t, count(`SELECT count(*) FROM series WHERE id = $1`, privateSeries))
	assert.Equal(t, 1, count(`SELECT count(*) FROM events WHERE id = $1 AND venue_id = $2`, milkBoyEvent, milkBoy))
	assert.Equal(t, 1, count(`SELECT count(*) FROM events WHERE id = $1 AND venue_id = $2`, taipeiEvent, taipeiArenaAgain))
	assert.Zero(t, count(`SELECT count(*) FROM venues WHERE id = $1`, taipeiArena))
	assert.Zero(t, count(`SELECT count(*) FROM concert_candidates WHERE id = $1`, candidateID), "the row that would collide is removed")
	assert.Equal(t, 1, count(`SELECT count(*) FROM concert_candidates WHERE id = $1`, unresolvedCandidateID))
	_, area, zone, place := read(capitolTheatre)
	assert.Equal(t, "SG-01", *area)
	assert.Equal(t, "Asia/Singapore", *zone)
	assert.Nil(t, place, "the place elsewhere is dropped")
	_, area, _, place = read(taipeiArenaAgain)
	assert.Equal(t, "TW-TPE", *area)
	assert.NotNil(t, place, "a right place is kept")
	listed, area, _, _ := read(singaporeIndoor)
	assert.Equal(t, "SINGAPORE INDOOR STADIUM", listed)
	assert.Equal(t, "SG-01", *area)
}
