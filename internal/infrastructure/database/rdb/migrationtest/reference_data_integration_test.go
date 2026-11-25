package migrationtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/testutil"
)

// TestReferenceData applies every schema migration and then the reference
// data directory the way the backend-reference-data AtlasMigration does
// (database-scoped, its own revisions schema, the baseline version), and
// checks that the seed lands, that applying the directory again does
// nothing, and that running the seed file a second time changes no row.
func TestReferenceData(t *testing.T) {
	ctx := context.Background()
	atlasPath, err := exec.LookPath("atlas")
	require.NoError(t, err, "atlas CLI is required to apply migrations")
	migrationsDir, err := filepath.Abs("../../../../../k8s/atlas/base/migrations")
	require.NoError(t, err)
	referenceDir, err := filepath.Abs("../../../../../k8s/atlas/base/reference-data")
	require.NoError(t, err)

	db := testutil.NewEmptyDatabase(t)
	atlas := func(args ...string) string {
		t.Helper()
		unlock := testutil.LockMigrations(t)
		out, err := exec.CommandContext(ctx, atlasPath, args...).CombinedOutput()
		unlock()
		require.NoError(t, err, "atlas %v: %s", args, out)
		return string(out)
	}
	atlas("migrate", "apply", "--url", db.URL("app"), "--dir", "file://"+migrationsDir)
	applyReference := func() string {
		return atlas("migrate", "apply", "--url", db.URL(""), "--dir", "file://"+referenceDir,
			"--revisions-schema", "atlas_reference_data", "--baseline", "20261124120000")
	}
	applyReference()

	conn, err := pgx.Connect(ctx, db.URL("app"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close(context.Background())) })

	// fingerprint sums every reference row, so any changed value shows.
	fingerprint := func() string {
		t.Helper()
		var sum string
		require.NoError(t, conn.QueryRow(ctx, `
			SELECT md5(
			  (SELECT string_agg(a::text, '|' ORDER BY code) FROM areas a) ||
			  (SELECT string_agg(z::text, '|' ORDER BY name) FROM time_zones z) ||
			  (SELECT string_agg(c::text, '|' ORDER BY country_code, time_zone) FROM country_time_zones c) ||
			  (SELECT string_agg(m::text, '|' ORDER BY region_code, member_code) FROM area_memberships m))`).Scan(&sum))
		return sum
	}

	t.Run("the seed lands", func(t *testing.T) {
		var tokyoZone, tokyoName string
		require.NoError(t, conn.QueryRow(ctx, `SELECT time_zone, name_ja FROM areas WHERE code = 'JP-13' AND is_first_level`).Scan(&tokyoZone, &tokyoName))
		assert.Equal(t, "Asia/Tokyo", tokyoZone)
		assert.Equal(t, "東京都", tokyoName)
		var replacedBy string
		require.NoError(t, conn.QueryRow(ctx, `SELECT replaced_by_code FROM areas WHERE code = 'CN-HK'`).Scan(&replacedBy))
		assert.Equal(t, "HK", replacedBy)
		var kanto int
		require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM area_memberships WHERE region_code = 'JP-KANTO'`).Scan(&kanto))
		assert.Equal(t, 7, kanto)
	})

	before := fingerprint()
	t.Run("applying the directory again does nothing", func(t *testing.T) {
		assert.Contains(t, applyReference(), "No migration files to execute")
		assert.Equal(t, before, fingerprint())
	})
	t.Run("running the seed file again changes no row", func(t *testing.T) {
		files, err := filepath.Glob(filepath.Join(referenceDir, "*_seed_areas.sql"))
		require.NoError(t, err)
		require.NotEmpty(t, files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			require.NoError(t, err)
			_, err = conn.Exec(ctx, string(b))
			require.NoError(t, err, f)
		}
		assert.Equal(t, before, fingerprint())
	})
}
