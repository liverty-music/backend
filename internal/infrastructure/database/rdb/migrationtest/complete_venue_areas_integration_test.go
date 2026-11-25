package migrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// completeVenueAreasPrevious is the last migration before
	// 20261125015333_complete_venue_areas.sql.
	completeVenueAreasPrevious = "20261125011819"
	// completeVenueAreasVersion is 20261125015333_complete_venue_areas.sql.
	completeVenueAreasVersion = "20261125015333"
)

// TestMigration_CompleteVenueAreas loads venues with a lower-level code, a
// withdrawn code, a code that has no Area, a first-level code of a one-zone
// Area and of an Area without a zone, applies
// 20261125015333_complete_venue_areas.sql and checks the remap, the cleared
// code, the zones, the coordinates' fetch time, the names and the new
// constraints.
func TestMigration_CompleteVenueAreas(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(completeVenueAreasPrevious)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	// The reference rows the migration reads; production has the full seed.
	mustExec(`INSERT INTO time_zones (name, name_en) VALUES ('Asia/Tokyo', 'Japan'), ('Europe/Paris', 'Paris'),
		('America/Chicago', 'Chicago'), ('America/Denver', 'Denver'), ('Asia/Seoul', 'Seoul')`)
	mustExec(`INSERT INTO country_time_zones (country_code, time_zone) VALUES ('JP', 'Asia/Tokyo'), ('FR', 'Europe/Paris'),
		('US', 'America/Chicago'), ('US', 'America/Denver'), ('KR', 'Asia/Seoul')`)
	mustExec(`INSERT INTO areas (code, kind, country_code, name_en, time_zone) VALUES
		('JP-13', 'subdivision', 'JP', 'Tokyo', 'Asia/Tokyo'),
		('FR-NAQ', 'subdivision', 'FR', 'Nouvelle-Aquitaine', 'Europe/Paris'),
		('FR-IDF', 'subdivision', 'FR', 'Ile-de-France', 'Europe/Paris'),
		('US-TX', 'subdivision', 'US', 'Texas', NULL)`)
	mustExec(`INSERT INTO areas (code, kind, country_code, parent_code, name_en, time_zone) VALUES ('FR-33', 'subdivision', 'FR', 'FR-NAQ', 'Gironde', 'Europe/Paris')`)
	mustExec(`INSERT INTO areas (code, kind, country_code, replaced_by_code, name_en, time_zone) VALUES ('FR-75', 'subdivision', 'FR', 'FR-IDF', 'Paris', 'Europe/Paris')`)

	venues := map[string]struct {
		area, place, listed string
		coords              bool
	}{
		"gironde": {area: "FR-33", listed: "Rocher de Palmer"},
		"paris":   {area: "FR-75", place: "p-paris", listed: "Zenith", coords: true},
		"taiwan":  {area: "CN-TW", listed: "Legacy Taipei"},
		"tokyo":   {area: "JP-13", place: "p-tokyo", listed: "Zepp Haneda(TOKYO)", coords: true},
		"texas":   {area: "US-TX", listed: "Stubb's"},
		"unnamed": {area: "JP-13", place: "p-nolist", coords: true},
	}
	ids := map[string]string{}
	n := 0
	for key, v := range venues {
		n++
		ids[key] = id(500 + n)
		var place, listed *string
		if v.place != "" {
			place = &v.place
		}
		if v.listed != "" {
			listed = &v.listed
		}
		var lat, lng *float64
		if v.coords {
			la, ln := 35.0, 139.0
			lat, lng = &la, &ln
		}
		mustExec(`INSERT INTO venues (id, name, admin_area, google_place_id, latitude, longitude, listed_venue_name)
			VALUES ($1, $7, $2, $3, $4, $5, $6)`, ids[key], v.area, place, lat, lng, listed, "Map name "+ids[key])
	}

	before := time.Now()
	apply(completeVenueAreasVersion)

	read := func(key string) (area, zone *string, name string, fetch *time.Time) {
		t.Helper()
		require.NoError(t, conn.QueryRow(ctx, `SELECT admin_area, time_zone, name, coordinates_fetch_time FROM venues WHERE id = $1`, ids[key]).
			Scan(&area, &zone, &name, &fetch))
		return
	}

	t.Run("lower-level and withdrawn codes become their first-level Area", func(t *testing.T) {
		area, zone, _, _ := read("gironde")
		assert.Equal(t, "FR-NAQ", *area)
		assert.Equal(t, "Europe/Paris", *zone)
		area, _, _, _ = read("paris")
		assert.Equal(t, "FR-IDF", *area)
	})
	t.Run("a code with no Area is cleared", func(t *testing.T) {
		area, zone, _, _ := read("taiwan")
		assert.Nil(t, area)
		assert.Nil(t, zone)
	})
	t.Run("an Area without a zone leaves the venue's zone unknown", func(t *testing.T) {
		area, zone, _, _ := read("texas")
		assert.Equal(t, "US-TX", *area)
		assert.Nil(t, zone)
	})
	t.Run("coordinates count as fetched 25 days ago", func(t *testing.T) {
		_, _, _, fetch := read("tokyo")
		require.NotNil(t, fetch)
		assert.WithinDuration(t, before.Add(-25*24*time.Hour), *fetch, time.Minute)
		_, _, _, fetch = read("gironde")
		assert.Nil(t, fetch, "no coordinates, no fetch time")
	})
	t.Run("a venue with a place is named by its listed name", func(t *testing.T) {
		_, _, name, _ := read("tokyo")
		assert.Equal(t, "Zepp Haneda(TOKYO)", name)
		_, _, name, _ = read("unnamed")
		assert.Equal(t, "Map name "+ids["unnamed"], name, "no listed name to take")
		_, _, name, _ = read("gironde")
		assert.Equal(t, "Map name "+ids["gironde"], name, "no place, name unchanged")
	})
	t.Run("only a first-level Area can be stored", func(t *testing.T) {
		_, err := conn.Exec(ctx, `UPDATE venues SET admin_area = 'FR-33' WHERE id = $1`, ids["texas"])
		assert.Error(t, err)
	})
	t.Run("only a zone of the venue's country can be stored", func(t *testing.T) {
		_, err := conn.Exec(ctx, `UPDATE venues SET time_zone = 'Asia/Seoul' WHERE id = $1`, ids["texas"])
		assert.Error(t, err)
		_, err = conn.Exec(ctx, `UPDATE venues SET time_zone = 'America/Denver' WHERE id = $1`, ids["texas"])
		assert.NoError(t, err)
	})
	t.Run("coordinates need their fetch time", func(t *testing.T) {
		_, err := conn.Exec(ctx, `UPDATE venues SET latitude = 30, longitude = 140 WHERE id = $1`, ids["texas"])
		assert.Error(t, err)
	})
}
