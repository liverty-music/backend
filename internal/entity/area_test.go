package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

func subdivision(code string) *entity.Area {
	return &entity.Area{Code: code, Kind: entity.AreaKindSubdivision, CountryCode: code[:2], NameEN: code}
}

func TestArea_IsFirstLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		area *entity.Area
		want bool
	}{
		// @spec components/entity/area "Prefecture"
		{name: "Prefecture", area: subdivision("JP-13"), want: true},
		// @spec components/entity/area "Territory without subdivisions"
		{name: "Territory without subdivisions", area: &entity.Area{Code: "HK", Kind: entity.AreaKindCountry, CountryCode: "HK", NameEN: "Hong Kong"}, want: true},
		// @spec components/entity/area "Lower-level subdivision"
		{name: "Lower-level subdivision", area: &entity.Area{Code: "FR-33", Kind: entity.AreaKindSubdivision, CountryCode: "FR", ParentCode: new("FR-NAQ"), NameEN: "Gironde"}, want: false},
		// @spec components/entity/area "Withdrawn code"
		{name: "Withdrawn code", area: &entity.Area{Code: "FR-75", Kind: entity.AreaKindSubdivision, CountryCode: "FR", ReplacedByCode: new("FR-IDF"), NameEN: "Paris"}, want: false},
		// @spec components/entity/area "Region"
		{name: "Region", area: &entity.Area{Code: "JP-KANTO", Kind: entity.AreaKindRegion, CountryCode: "JP", NameEN: "Kanto"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.area.IsFirstLevel())
		})
	}
}

func TestArea_Validate_Code(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		area    *entity.Area
		wantErr bool
	}{
		// @spec components/entity/area "Subdivision code"
		{name: "Subdivision code", area: subdivision("US-CA")},
		// @spec components/entity/area "Country code as an Area"
		{name: "Country code as an Area", area: &entity.Area{Code: "MO", Kind: entity.AreaKindCountry, CountryCode: "MO", NameEN: "Macao"}},
		// @spec components/entity/area "Region code too short"
		{name: "Region code too short", area: &entity.Area{Code: "JP-KAN", Kind: entity.AreaKindRegion, CountryCode: "JP", NameEN: "Kan"}, wantErr: true},
		// @spec components/entity/area "Code of another country"
		{name: "Code of another country", area: &entity.Area{Code: "KR-11", Kind: entity.AreaKindSubdivision, CountryCode: "JP", NameEN: "Seoul"}, wantErr: true},
		{name: "Region code that is a subdivision code", area: &entity.Area{Code: "JP-13", Kind: entity.AreaKindRegion, CountryCode: "JP", NameEN: "Tokyo"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.area.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestArea_Group(t *testing.T) {
	t.Parallel()
	kanto := &entity.Area{Code: "JP-KANTO", Kind: entity.AreaKindRegion, CountryCode: "JP", NameEN: "Kanto"}

	t.Run("Prefecture in its region", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area "Prefecture in its region"
		tokyo := subdivision("JP-13")
		require.NoError(t, kanto.Group(tokyo))
		assert.Contains(t, tokyo.RegionCodes, "JP-KANTO")
		assert.True(t, tokyo.IsFirstLevel())
		assert.Nil(t, tokyo.ParentCode)
	})
	t.Run("Lower-level Area in a region", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area "Lower-level Area in a region"
		gironde := &entity.Area{Code: "FR-33", Kind: entity.AreaKindSubdivision, CountryCode: "FR", ParentCode: new("FR-NAQ"), NameEN: "Gironde"}
		assert.Error(t, kanto.Group(gironde))
	})
}

func TestArea_ValidateTimeZone(t *testing.T) {
	t.Parallel()

	usZones := []string{"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles"}
	tests := []struct {
		name    string
		area    *entity.Area
		zones   []string
		wantErr bool
	}{
		// @spec components/entity/area "Country with one zone"
		{name: "Country with one zone", area: &entity.Area{Code: "JP-13", TimeZone: new("Asia/Tokyo")}, zones: []string{"Asia/Tokyo"}},
		{name: "Country with one zone but no zone on the area", area: &entity.Area{Code: "JP-13"}, zones: []string{"Asia/Tokyo"}, wantErr: true},
		// @spec components/entity/area "State wholly in one zone"
		{name: "State wholly in one zone", area: &entity.Area{Code: "US-CA", TimeZone: new("America/Los_Angeles")}, zones: usZones},
		// @spec components/entity/area "State across two zones"
		{name: "State across two zones", area: &entity.Area{Code: "US-TX"}, zones: usZones},
		// @spec components/entity/area "Zone of another country"
		{name: "Zone of another country", area: &entity.Area{Code: "KR-11", TimeZone: new("Asia/Tokyo")}, zones: []string{"Asia/Seoul"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.area.Kind = entity.AreaKindSubdivision
			err := tt.area.ValidateTimeZone(tt.zones)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestArea_Validate_ReferencePoint(t *testing.T) {
	t.Parallel()

	legacy := entity.ReferencePointLegacy
	point := &entity.Coordinates{Latitude: 33.6064, Longitude: 130.4181}

	t.Run("Prefecture from the former table", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area "Prefecture from the former table"
		a := subdivision("JP-40")
		a.ReferencePoint, a.ReferencePointSource = point, &legacy
		require.NoError(t, a.Validate())
		assert.Equal(t, entity.ReferencePointLegacy, *a.ReferencePointSource)
	})
	t.Run("Point without a source", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area "Point without a source"
		a := subdivision("JP-40")
		a.ReferencePoint = point
		assert.Error(t, a.Validate())
	})
	t.Run("No point known", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area "No point known"
		a := subdivision("JP-40")
		require.NoError(t, a.Validate())
		assert.Nil(t, a.ReferencePoint)
	})
}

func TestArea_Names(t *testing.T) {
	t.Parallel()

	t.Run("Japanese name present", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area "Japanese name present"
		a := &entity.Area{Code: "JP-13", NameEN: "Tokyo", NameJA: new("東京都")}
		assert.Equal(t, "東京都", a.Name("ja"))
	})
	t.Run("No Japanese name", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area "No Japanese name"
		a := &entity.Area{Code: "FR-NAQ", NameEN: "Nouvelle-Aquitaine"}
		assert.Equal(t, "Nouvelle-Aquitaine", a.Name("ja"))
	})
	t.Run("English viewer", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area "English viewer"
		z := &entity.AreaTimeZone{Name: "Asia/Seoul", NameEN: "Seoul", NameJA: new("ソウル")}
		assert.Equal(t, "Seoul", z.DisplayName("en"))
	})
	t.Run("Japan's zone", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/area "Japan's zone"
		z := &entity.AreaTimeZone{Name: "Asia/Tokyo", NameEN: "Japan", NameJA: new("日本")}
		assert.Equal(t, "日本", z.DisplayName("ja"))
		assert.Equal(t, "Japan", z.DisplayName("en"))
	})
}
