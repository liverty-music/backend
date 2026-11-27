package migrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// otherListedNamesPrevious is the last migration before
	// 20261127044913_add_event_other_listed_venue_names.sql.
	otherListedNamesPrevious = "20261126152558"
	// otherListedNamesVersion is
	// 20261127044913_add_event_other_listed_venue_names.sql.
	otherListedNamesVersion = "20261127044913"
)

// TestMigration_AddEventOtherListedVenueNames loads an event, applies
// 20261127044913_add_event_other_listed_venue_names.sql and checks that the
// event exists with no other listed venue names and that a new event can be
// stored with some.
func TestMigration_AddEventOtherListedVenueNames(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(otherListedNamesPrevious)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO time_zones (name, name_en) VALUES ('Asia/Tokyo', 'Japan') ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO country_time_zones (country_code, time_zone) VALUES ('JP', 'Asia/Tokyo') ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO areas (code, kind, country_code, name_en) VALUES ('JP-27', 'subdivision', 'JP', 'Osaka') ON CONFLICT DO NOTHING`)
	venue, series, before, after := id(900), id(901), id(902), id(903)
	mustExec(`INSERT INTO venues (id, name, admin_area, time_zone) VALUES ($1, '京セラドーム大阪', 'JP-27', 'Asia/Tokyo')`, venue)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour', 'TOUR')`, series)
	mustExec(`INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date) VALUES ($1, $2, $3, '京セラドーム大阪', '2026-12-19')`,
		before, series, venue)

	apply(otherListedNamesVersion)

	var names []string
	require.NoError(t, conn.QueryRow(ctx, `SELECT other_listed_venue_names FROM events WHERE id = $1`, before).Scan(&names))
	assert.Empty(t, names, "an event stored before the migration has no other listed venue names")

	mustExec(`INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date, other_listed_venue_names)
		VALUES ($1, $2, $3, '京セラドーム大阪', '2026-12-20', ARRAY['Kyocera Dome Osaka'])`, after, series, venue)
	require.NoError(t, conn.QueryRow(ctx, `SELECT other_listed_venue_names FROM events WHERE id = $1`, after).Scan(&names))
	assert.Equal(t, []string{"Kyocera Dome Osaka"}, names)
}
