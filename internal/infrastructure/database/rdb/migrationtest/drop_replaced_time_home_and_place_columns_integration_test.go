package migrationtest

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// dropReplacedPrevious is the last migration before
	// 20261126152558_drop_replaced_time_home_and_place_columns.sql.
	dropReplacedPrevious = "20261126141736"
	// dropReplacedVersion is
	// 20261126152558_drop_replaced_time_home_and_place_columns.sql.
	dropReplacedVersion = "20261126152558"
)

// TestMigration_DropReplacedColumns loads rows that still hold values in the
// columns and the table that releases 1 and 2 replaced, applies
// 20261126152558_drop_replaced_time_home_and_place_columns.sql and checks
// that exactly those objects are gone, that the values the running code reads
// are unchanged, and that a connection which ran a statement before the
// migration runs it again afterwards.
func TestMigration_DropReplacedColumns(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(dropReplacedPrevious)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	// texts returns the one text column of the query's rows.
	texts := func(sql string) []string {
		t.Helper()
		rows, err := conn.Query(ctx, sql)
		require.NoError(t, err, sql)
		values, err := pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err, sql)
		return values
	}
	columns := func() []string {
		return texts(`SELECT table_name || '.' || column_name FROM information_schema.columns
			WHERE table_schema = 'app' ORDER BY 1`)
	}
	constraints := func() []string {
		return texts(`SELECT conrelid::regclass::text || ' ' || conname FROM pg_constraint
			WHERE connamespace = 'app'::regnamespace AND conrelid <> 0 AND contype <> 'n' ORDER BY 1`)
	}
	indexes := func() []string {
		return texts(`SELECT tablename || ' ' || indexname FROM pg_indexes WHERE schemaname = 'app' ORDER BY 1`)
	}

	mustExec(`INSERT INTO time_zones (name, name_en) VALUES ('Asia/Tokyo', 'Japan') ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO country_time_zones (country_code, time_zone) VALUES ('JP', 'Asia/Tokyo') ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO areas (code, kind, country_code, name_en) VALUES ('JP-13', 'subdivision', 'JP', 'Tokyo') ON CONFLICT DO NOTHING`)

	venue, series, artist := id(800), id(801), id(802)
	mustExec(`INSERT INTO venues (id, name, admin_area, time_zone) VALUES ($1, 'Zepp Haneda', 'JP-13', 'Asia/Tokyo')`, venue)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour', 'TOUR')`, series)
	mustExec(`INSERT INTO artists (id, name, mbid) VALUES ($1, 'Artist', '00000000-0000-0000-0000-000000000010')`, artist)

	event, sale, candidate, rejected := id(810), id(811), id(812), id(813)
	mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date, start_time, open_time, local_start_time, local_open_time)
		VALUES ($1, $2, $3, '2026-12-01', '2026-12-01T09:00:00Z', '2026-12-01T08:00:00Z', '18:00', '17:00')`, event, series, venue)
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, sale_start_time, sale_end_time, time_zone, local_start_time, local_end_time, find_time)
		VALUES ($1, $2, 'Presale', 1, '2026-11-01T03:00:00Z', '2026-11-02T03:00:00Z', 'Asia/Tokyo', '2026-11-01 12:00', '2026-11-02 12:00', now())`, sale, series)
	mustExec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, local_date, start_time, open_time, local_start_time, listed_venue_name,
			resolved_place_id, resolved_venue_name, resolved_admin_area, resolved_latitude, resolved_longitude, time_zone)
		VALUES ($1, $2, $3, 'T', '2026-12-01', '2026-12-01T09:00:00Z', '2026-12-01T08:00:00Z', '18:00', 'Hall', 'place-1', 'Hall', 'JP-13', 35.5, 139.7, 'Asia/Tokyo')`,
		candidate, artist, series)
	mustExec(`INSERT INTO rejected_concerts (id, artist_id, artist_name, title, local_date, start_time, open_time, local_start_time, listed_venue_name,
			resolved_place_id, resolved_venue_name, resolved_admin_area, reason)
		VALUES ($1, $2, 'Artist', 'T', '2026-12-01', '2026-12-01T09:00:00Z', '2026-12-01T08:00:00Z', '18:00', 'Hall', 'place-1', 'Hall', 'JP-13', 'no')`,
		rejected, artist)

	// A home from before release 1, still referenced by its user, one left
	// behind by a deleted user, and the user's home as release 1 stores it.
	home, orphan, user := id(820), id(821), id(822)
	mustExec(`INSERT INTO homes (id, country_code, level_1) VALUES ($1, 'JP', 'JP-13'), ($2, 'JP', 'JP-13')`, home, orphan)
	mustExec(`INSERT INTO users (id, provider_user_id, email, name, preferred_language, home_id) VALUES ($1, 'p1', 'fan@example.com', 'fan', 'ja', $2)`, user, home)
	mustExec(`INSERT INTO user_home_areas (user_id, area_code) VALUES ($1, 'JP-13')`, user)

	// updateLanguage is shaped as the released code's user update: its
	// RETURNING * names every users column, home_id included, and the
	// connection caches it as a prepared statement.
	const updateLanguage = `WITH updated AS (UPDATE users SET preferred_language = $2 WHERE id = $1 RETURNING *)
		SELECT u.id::text, u.preferred_language, uh.area_code FROM updated u LEFT JOIN user_home_areas uh ON uh.user_id = u.id`
	runUpdateLanguage := func(language string) {
		t.Helper()
		var gotID, gotLanguage, gotArea string
		require.NoError(t, conn.QueryRow(ctx, updateLanguage, user, language).Scan(&gotID, &gotLanguage, &gotArea))
		assert.Equal(t, []string{user, language, "JP-13"}, []string{gotID, gotLanguage, gotArea})
	}
	runUpdateLanguage("en")

	columnsBefore, constraintsBefore, indexesBefore := columns(), constraints(), indexes()

	apply(dropReplacedVersion)

	t.Run("exactly the replaced columns and homes are gone", func(t *testing.T) {
		assert.ElementsMatch(t, []string{
			"concert_candidates.open_time",
			"concert_candidates.resolved_admin_area",
			"concert_candidates.resolved_latitude",
			"concert_candidates.resolved_longitude",
			"concert_candidates.resolved_venue_name",
			"concert_candidates.start_time",
			"events.open_time",
			"events.start_time",
			"homes.centroid_latitude",
			"homes.centroid_longitude",
			"homes.country_code",
			"homes.id",
			"homes.level_1",
			"homes.level_2",
			"rejected_concerts.open_time",
			"rejected_concerts.resolved_admin_area",
			"rejected_concerts.resolved_venue_name",
			"rejected_concerts.start_time",
			"ticket_sales.sale_end_time",
			"ticket_sales.sale_start_time",
			"users.home_id",
		}, removed(columnsBefore, columns()))
		assert.Empty(t, removed(columns(), columnsBefore), "no column is added")
	})
	t.Run("no constraint or index of a kept column goes with them", func(t *testing.T) {
		assert.ElementsMatch(t, []string{
			"homes chk_homes_country_code_length",
			"homes chk_homes_id_uuidv7",
			"homes chk_homes_level_1_length",
			"homes chk_homes_level_2_length",
			"homes homes_pkey",
			"users fk_users_home_id",
		}, removed(constraintsBefore, constraints()))
		assert.Equal(t, []string{"homes homes_pkey"}, removed(indexesBefore, indexes()))
	})
	t.Run("the values the running code reads are unchanged", func(t *testing.T) {
		assert.Equal(t, []string{"18:00:00 17:00:00"},
			texts(`SELECT local_start_time::text || ' ' || local_open_time::text FROM events`))
		assert.Equal(t, []string{"Asia/Tokyo 2026-11-01 12:00:00 2026-11-02 12:00:00"},
			texts(`SELECT time_zone || ' ' || local_start_time::text || ' ' || local_end_time::text FROM ticket_sales`))
		assert.Equal(t, []string{"place-1 18:00:00 Asia/Tokyo"},
			texts(`SELECT resolved_place_id || ' ' || local_start_time::text || ' ' || time_zone FROM concert_candidates`))
		assert.Equal(t, []string{"place-1 18:00:00"},
			texts(`SELECT resolved_place_id || ' ' || local_start_time::text FROM rejected_concerts`))
		assert.Equal(t, []string{"JP-13"}, texts(`SELECT area_code FROM user_home_areas`))
	})
	t.Run("a statement cached before the migration still runs", func(t *testing.T) {
		runUpdateLanguage("ja")
	})
}

// removed returns the values of before that after does not hold.
func removed(before, after []string) []string {
	var gone []string
	for _, v := range before {
		if !slices.Contains(after, v) {
			gone = append(gone, v)
		}
	}
	return gone
}
