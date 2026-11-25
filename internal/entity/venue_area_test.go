package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
)

func TestVenue_CheckArea(t *testing.T) {
	t.Parallel()

	area := func(code string, zone *string) *entity.Area {
		kind := entity.AreaKindSubdivision
		if len(code) == 2 {
			kind = entity.AreaKindCountry
		}
		return &entity.Area{Code: code, Kind: kind, CountryCode: code[:2], NameEN: code, TimeZone: zone}
	}
	lower := &entity.Area{Code: "FR-33", Kind: entity.AreaKindSubdivision, CountryCode: "FR", ParentCode: new("FR-NAQ"), NameEN: "Gironde"}
	withdrawn := &entity.Area{Code: "FR-75", Kind: entity.AreaKindSubdivision, CountryCode: "FR", ReplacedByCode: new("FR-IDF"), NameEN: "Paris"}
	region := &entity.Area{Code: "JP-KANTO", Kind: entity.AreaKindRegion, CountryCode: "JP", NameEN: "Kanto"}
	jp := []string{"Asia/Tokyo"}
	us := []string{"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles"}

	tests := []struct {
		name    string
		area    *string // the venue's admin area
		zone    *string // the venue's time zone
		got     *entity.Area
		zones   []string
		wantErr bool
	}{
		// @spec components/entity/venue "Valid admin area"
		{name: "Valid admin area", area: new("JP-13"), zone: new("Asia/Tokyo"), got: area("JP-13", new("Asia/Tokyo")), zones: jp},
		// @spec components/entity/venue "Territory without subdivisions"
		// @spec components/entity/venue "Zone of a territory"
		{name: "Territory without subdivisions", area: new("HK"), zone: new("Asia/Hong_Kong"), got: area("HK", new("Asia/Hong_Kong")), zones: []string{"Asia/Hong_Kong"}},
		// @spec components/entity/venue "Free-text admin area is invalid"
		{name: "Free-text admin area is invalid", area: new("東京都"), zone: new("Asia/Tokyo"), wantErr: true},
		// @spec components/entity/venue "Well-formed code that is no Area"
		{name: "Well-formed code that is no Area", area: new("JP-99"), zone: new("Asia/Tokyo"), wantErr: true},
		// @spec components/entity/venue "Lower-level code"
		{name: "Lower-level code", area: new("FR-33"), zone: new("Europe/Paris"), got: lower, zones: []string{"Europe/Paris"}, wantErr: true},
		// @spec components/entity/venue "Withdrawn code"
		{name: "Withdrawn code", area: new("FR-75"), zone: new("Europe/Paris"), got: withdrawn, zones: []string{"Europe/Paris"}, wantErr: true},
		// @spec components/entity/venue "Region code"
		{name: "Region code", area: new("JP-KANTO"), zone: new("Asia/Tokyo"), got: region, zones: jp, wantErr: true},
		// @spec components/entity/venue "Missing admin area"
		{name: "Missing admin area", zone: new("Asia/Tokyo"), wantErr: true},
		// @spec components/entity/venue "Zone of the area's country"
		{name: "Zone of the area's country", area: new("KR-11"), zone: new("Asia/Seoul"), got: area("KR-11", new("Asia/Seoul")), zones: []string{"Asia/Seoul"}},
		// @spec components/entity/venue "Zone of another country"
		{name: "Zone of another country", area: new("JP-13"), zone: new("Asia/Seoul"), got: area("JP-13", new("Asia/Tokyo")), zones: jp, wantErr: true},
		// @spec components/entity/venue "Zone other than the Area's own"
		{name: "Zone other than the Area's own", area: new("US-CA"), zone: new("America/New_York"), got: area("US-CA", new("America/Los_Angeles")), zones: us, wantErr: true},
		// @spec components/entity/venue "Area without a time zone of its own"
		{name: "Area without a time zone of its own, Chicago", area: new("US-TX"), zone: new("America/Chicago"), got: area("US-TX", nil), zones: us},
		{name: "Area without a time zone of its own, Denver", area: new("US-TX"), zone: new("America/Denver"), got: area("US-TX", nil), zones: us},
		// @spec components/entity/venue "Missing time zone"
		{name: "Missing time zone", area: new("JP-13"), got: area("JP-13", new("Asia/Tokyo")), zones: jp, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := (&entity.Venue{AdminArea: tt.area, TimeZone: tt.zone}).CheckArea(tt.got, tt.zones)
			if tt.wantErr {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestVenue_Coordinates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)
	point := &entity.Coordinates{Latitude: 35.55, Longitude: 139.78}

	t.Run("Recent coordinates", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/venue "Recent coordinates"
		fetch := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
		assert.False(t, (&entity.Venue{Coordinates: point, CoordinatesFetchTime: &fetch}).CoordinatesExpired(now))
	})
	t.Run("Coordinates older than 30 days", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/venue "Coordinates older than 30 days"
		fetch := time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)
		assert.True(t, (&entity.Venue{Coordinates: point, CoordinatesFetchTime: &fetch}).CoordinatesExpired(now))
	})
	t.Run("Coordinates without a fetch time", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/venue "Coordinates without a fetch time"
		assert.ErrorIs(t, (&entity.Venue{Coordinates: point}).CheckCoordinates(), apperr.ErrInvalidArgument)
	})
}

func TestVenue_Location(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Asia/Seoul", (&entity.Venue{TimeZone: new("Asia/Seoul")}).Location().String())
	assert.Nil(t, (&entity.Venue{}).Location())
}
