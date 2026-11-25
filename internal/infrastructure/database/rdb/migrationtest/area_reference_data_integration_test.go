package migrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// areaReferenceDataVersion is 20261124100811_add_area_reference_data.sql.
const areaReferenceDataVersion = "20261124100811"

// TestMigration_AreaReferenceData applies
// 20261124100811_add_area_reference_data.sql to an empty schema and checks
// that a table referencing (code, is_first_level) with a constant TRUE column
// rejects a lower-level, a withdrawn and a region code and accepts a
// first-level one, and that fk_areas_time_zone rejects a zone of another
// country.
func TestMigration_AreaReferenceData(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(areaReferenceDataVersion)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO time_zones (name, name_en) VALUES ('Asia/Tokyo', 'Japan'), ('Europe/Paris', 'Paris')`)
	mustExec(`INSERT INTO country_time_zones (country_code, time_zone) VALUES ('JP', 'Asia/Tokyo'), ('FR', 'Europe/Paris')`)
	mustExec(`INSERT INTO areas (code, kind, country_code, name_en, time_zone) VALUES
		('JP-13', 'subdivision', 'JP', 'Tokyo', 'Asia/Tokyo'),
		('FR-NAQ', 'subdivision', 'FR', 'Nouvelle-Aquitaine', 'Europe/Paris'),
		('JP-KANTO', 'region', 'JP', 'Kanto', NULL)`)
	mustExec(`INSERT INTO areas (code, kind, country_code, parent_code, name_en) VALUES ('FR-33', 'subdivision', 'FR', 'FR-NAQ', 'Gironde')`)
	mustExec(`INSERT INTO areas (code, kind, country_code, replaced_by_code, name_en) VALUES ('JP-99', 'subdivision', 'JP', 'JP-13', 'Withdrawn')`)

	// A table that follows Decision 1: a code and a constant TRUE column.
	mustExec(`CREATE TABLE first_level_refs (
		area_code TEXT NOT NULL,
		area_first_level BOOLEAN NOT NULL DEFAULT TRUE CHECK (area_first_level),
		FOREIGN KEY (area_code, area_first_level) REFERENCES areas (code, is_first_level))`)

	t.Run("a first-level code is accepted", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO first_level_refs (area_code) VALUES ('JP-13')`)
		assert.NoError(t, err)
	})
	for name, code := range map[string]string{
		"a lower-level code is rejected": "FR-33",
		"a withdrawn code is rejected":   "JP-99",
		"a region code is rejected":      "JP-KANTO",
		"an unknown code is rejected":    "JP-98",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := conn.Exec(ctx, `INSERT INTO first_level_refs (area_code) VALUES ($1)`, code)
			assert.Error(t, err)
		})
	}
	t.Run("an area cannot lose first-level status while referenced", func(t *testing.T) {
		_, err := conn.Exec(ctx, `UPDATE areas SET replaced_by_code = 'FR-NAQ' WHERE code = 'JP-13'`)
		assert.Error(t, err)
	})
	t.Run("a region contains first-level members only", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO area_memberships (region_code, member_code) VALUES ('JP-KANTO', 'JP-13')`)
		assert.NoError(t, err)
		_, err = conn.Exec(ctx, `INSERT INTO area_memberships (region_code, member_code) VALUES ('JP-KANTO', 'FR-33')`)
		assert.Error(t, err)
	})
	t.Run("an area cannot have a zone of another country", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO areas (code, kind, country_code, name_en, time_zone) VALUES ('JP-14', 'subdivision', 'JP', 'Kanagawa', 'Europe/Paris')`)
		assert.Error(t, err)
	})
}
