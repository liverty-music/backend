package rdb_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// The test database holds the generated reference data
// (k8s/atlas/base/reference-data), so these tests read the real seed.

func TestAreaRepository_Resolve(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := rdb.NewAreaRepository(newTestDB(t))

	tests := []struct {
		name    string
		text    string
		country *string
		want    string
	}{
		// @spec components/entity/area/resolve "First-level code"
		{name: "First-level code", text: "JP-13", want: "JP-13"},
		// @spec components/entity/area/resolve "Code in lower case"
		{name: "Code in lower case", text: "us-ca", want: "US-CA"},
		// @spec components/entity/area/resolve "Lower-level code"
		{name: "Lower-level code", text: "FR-33", want: "FR-NAQ"},
		// @spec components/entity/area/resolve "Withdrawn code"
		{name: "Withdrawn code", text: "FR-75", want: "FR-IDF"},
		// @spec components/entity/area/resolve "Territory by either code"
		{name: "Territory by its own code", text: "HK", want: "HK"},
		{name: "Territory by its subdivision code", text: "CN-HK", want: "HK"},
		// @spec components/entity/area/resolve "English name"
		{name: "English name", text: "california", want: "US-CA"},
		// @spec components/entity/area/resolve "Japanese name with and without its suffix"
		{name: "Japanese name with its suffix", text: "愛知県", want: "JP-23"},
		{name: "Japanese name without its suffix", text: "東京", want: "JP-13"},
		// @spec components/entity/area/resolve "Shared name with a country"
		{name: "Shared name with a country", text: "Victoria", country: new("AU"), want: "AU-VIC"},
		{name: "Spaces around the text", text: "  JP-13 ", want: "JP-13"},
		{name: "Name in the form of a code", text: "al-qassim", want: "SA-05"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := repo.Resolve(ctx, tt.text, tt.country)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Code)
			assert.True(t, got.IsFirstLevel())
		})
	}

	notFound := []struct {
		name    string
		text    string
		country *string
	}{
		// @spec components/entity/area/resolve "Region code"
		{name: "Region code", text: "JP-KANTO"},
		// @spec components/entity/area/resolve "Region name"
		{name: "Region name", text: "関東"},
		// @spec components/entity/area/resolve "Well-formed code that is no Area"
		{name: "Well-formed code that is no Area", text: "JP-99"},
		// @spec components/entity/area/resolve "Country that has subdivisions of its own"
		{name: "Country that has subdivisions of its own, by code", text: "CN-TW"},
		{name: "Country that has subdivisions of its own, by name", text: "Taiwan"},
		// @spec components/entity/area/resolve "Name without its diacritics"
		{name: "Name without its diacritics", text: "Cordoba"},
		// @spec components/entity/area/resolve "Shared name without a country"
		{name: "Shared name without a country", text: "Victoria"},
	}
	for _, tt := range notFound {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := repo.Resolve(ctx, tt.text, tt.country)
			assert.ErrorIs(t, err, apperr.ErrNotFound)
		})
	}

	t.Run("Empty text", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area/resolve "Empty text"
		_, err := repo.Resolve(ctx, "   ", nil)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestAreaRepository_Resolve_SameNameTwiceInOneCountry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	repo := rdb.NewAreaRepository(db)

	// @spec components/entity/area/resolve "Same name twice in one country"
	_, err := db.Pool.Exec(ctx, `INSERT INTO areas (code, kind, country_code, name_en) VALUES
		('XA-1', 'subdivision', 'XA', 'Twin'), ('XA-2', 'subdivision', 'XA', 'Twin')`)
	require.NoError(t, err)

	_, err = repo.Resolve(ctx, "Twin", new("XA"))
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

func TestAreaRepository_ListByCountry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := rdb.NewAreaRepository(newTestDB(t))

	codes := func(areas []*entity.Area) []string {
		out := make([]string, 0, len(areas))
		for _, a := range areas {
			out = append(out, a.Code)
		}
		return out
	}
	zoneNames := func(zones []*entity.AreaTimeZone) []string {
		out := make([]string, 0, len(zones))
		for _, z := range zones {
			out = append(out, z.Name)
		}
		return out
	}

	t.Run("Japan", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area/list-by-country "Japan"
		got, err := repo.ListByCountry(ctx, "JP")
		require.NoError(t, err)
		require.Len(t, got.Areas, 47)
		assert.Equal(t, "JP-01", got.Areas[0].Code)
		assert.Equal(t, "JP-47", got.Areas[46].Code)
		for _, a := range got.Areas {
			require.NotNil(t, a.TimeZone, a.Code)
			assert.Equal(t, "Asia/Tokyo", *a.TimeZone)
		}
		assert.Contains(t, got.Areas[12].RegionCodes, "JP-KANTO", "JP-13 is grouped into Kanto")
		assert.Len(t, got.Regions, 8)
		assert.Contains(t, codes(got.Regions), "JP-KANTO")
		assert.Equal(t, []string{"Asia/Tokyo"}, zoneNames(got.TimeZones))
	})
	t.Run("Country with several time zones", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area/list-by-country "Country with several time zones"
		got, err := repo.ListByCountry(ctx, "US")
		require.NoError(t, err)
		byCode := map[string]*entity.Area{}
		for _, a := range got.Areas {
			byCode[a.Code] = a
		}
		require.NotNil(t, byCode["US-CA"].TimeZone)
		assert.Equal(t, "America/Los_Angeles", *byCode["US-CA"].TimeZone)
		assert.Nil(t, byCode["US-TX"].TimeZone)
		assert.Subset(t, zoneNames(got.TimeZones), []string{"America/Los_Angeles", "America/Chicago"})
	})
	t.Run("Lower levels left out", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area/list-by-country "Lower levels left out"
		got, err := repo.ListByCountry(ctx, "FR")
		require.NoError(t, err)
		assert.Contains(t, codes(got.Areas), "FR-NAQ")
		assert.NotContains(t, codes(got.Areas), "FR-33")
		assert.NotContains(t, codes(got.Areas), "FR-75")
	})
	t.Run("Territory without subdivisions", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area/list-by-country "Territory without subdivisions"
		got, err := repo.ListByCountry(ctx, "HK")
		require.NoError(t, err)
		require.Len(t, got.Areas, 1)
		assert.Equal(t, entity.AreaKindCountry, got.Areas[0].Kind)
		assert.Empty(t, got.Regions)
		assert.Equal(t, []string{"Asia/Hong_Kong"}, zoneNames(got.TimeZones))
	})
	t.Run("Country without Areas", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area/list-by-country "Country without Areas"
		got, err := repo.ListByCountry(ctx, "XX")
		require.NoError(t, err)
		assert.Empty(t, got.Areas)
		assert.Empty(t, got.Regions)
		assert.Empty(t, got.TimeZones)
	})
	t.Run("Malformed country code", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area/list-by-country "Malformed country code"
		for _, c := range []string{"jp", "JPN"} {
			_, err := repo.ListByCountry(ctx, c)
			assert.ErrorIs(t, err, apperr.ErrInvalidArgument, c)
		}
	})
}

func TestAreaRepository_ListCountries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	got, err := rdb.NewAreaRepository(db).ListCountries(ctx)
	require.NoError(t, err)

	byCode := map[string]*entity.Country{}
	count := map[string]int{}
	listed := make([]string, 0, len(got))
	for _, c := range got {
		byCode[c.Code] = c
		count[c.Code]++
		listed = append(listed, c.Code)
	}
	assert.IsIncreasing(t, listed, "in code order")

	t.Run("Japan and Korea", func(t *testing.T) {
		// @spec components/entity/area/list-countries "Japan and Korea"
		assert.Equal(t, "Japan", byCode["JP"].NameEN)
		assert.Equal(t, "日本", *byCode["JP"].NameJA)
		assert.Equal(t, "South Korea", byCode["KR"].NameEN)
		assert.Equal(t, "韓国", *byCode["KR"].NameJA)
	})
	t.Run("Territory that is its own Area", func(t *testing.T) {
		// @spec components/entity/area/list-countries "Territory that is its own Area"
		assert.Equal(t, 1, count["HK"])
		assert.Equal(t, "Hong Kong SAR China", byCode["HK"].NameEN)
		assert.Equal(t, "中華人民共和国香港特別行政区", *byCode["HK"].NameJA)
	})
	t.Run("Country without first-level Areas", func(t *testing.T) {
		// @spec components/entity/area/list-countries "Country without first-level Areas"
		var without []string
		require.NoError(t, db.Pool.QueryRow(ctx, `
			SELECT COALESCE(array_agg(c.code), '{}') FROM countries c
			WHERE NOT EXISTS (SELECT 1 FROM areas a WHERE a.country_code = c.code AND a.is_first_level)`).Scan(&without))
		assert.Empty(t, without, "every listed country has a first-level Area")
		_, ok := byCode["AQ"]
		assert.Equal(t, hasFirstLevel(t, db, "AQ"), ok)
	})
}

func hasFirstLevel(t *testing.T, db *rdb.Database, country string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM areas WHERE country_code = $1 AND is_first_level)`, country).Scan(&ok))
	return ok
}
