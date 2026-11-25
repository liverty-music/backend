package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/geo"
)

func TestLocationQuery_Check(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	assert.NoError(t, entity.LocationQuery{From: from, To: from.Add(entity.MaxLocationDateRange)}.Check(), "exactly 30 days")
	assert.ErrorIs(t, entity.LocationQuery{From: from, To: from.Add(entity.MaxLocationDateRange + time.Nanosecond)}.Check(), apperr.ErrInvalidArgument)
}

func TestLocationQuery_NearbyGroups(t *testing.T) {
	t.Parallel()

	tokyo, yokohama, osaka := "JP-13", "JP-14", "JP-27"
	concertAt := func(id string, day int, area string, lat, lng float64) *entity.Concert {
		return &entity.Concert{
			ID:        id,
			LocalDate: time.Date(2026, 12, day, 0, 0, 0, 0, time.UTC),
			Venue:     &entity.Venue{AdminArea: &area, Coordinates: &entity.Coordinates{Latitude: lat, Longitude: lng}},
		}
	}
	home := concertAt("home", 1, tokyo, 35.6762, 139.6503)
	nearby := concertAt("nearby", 1, yokohama, 35.4437, 139.6380)
	awaySameDay := concertAt("away-1", 1, osaka, 34.6937, 135.5023)
	awayOnly := concertAt("away-2", 2, osaka, 34.6937, 135.5023)
	query := entity.LocationQuery{Location: &entity.GeoLocation{AdminArea: tokyo, Point: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}}}

	groups := query.NearbyGroups([]*entity.Concert{home, nearby, awaySameDay, awayOnly})

	require.Len(t, groups, 1, "a date with only away concerts is dropped")
	assert.Equal(t, []*entity.Concert{home}, groups[0].Home)
	assert.Equal(t, []*entity.Concert{nearby}, groups[0].Nearby)
	assert.Empty(t, groups[0].Away, "away concerts are never returned")
}

// TestGeoLocation_BoxHoldsEveryNearbyPoint checks the box margins: from a
// reference point at any latitude, every point on the box's edge is at least
// NearbyThresholdKm away, so no NEARBY venue lies outside it.
func TestGeoLocation_BoxHoldsEveryNearbyPoint(t *testing.T) {
	t.Parallel()

	for lat := -84.0; lat <= 84.0; lat += 1.0 {
		point := &entity.GeoLocation{AdminArea: "XX", Point: &entity.Coordinates{Latitude: lat, Longitude: 0}}
		box, ok := point.Box()
		require.True(t, ok)
		if len(box.Lng) == 1 && box.Lng[0] == [2]float64{-180, 180} {
			continue // every longitude
		}
		lo, hi := box.Lng[0][0], box.Lng[0][1]
		for i := 0; i <= 100; i++ {
			frac := float64(i) / 100
			edge := []struct{ lat, lng float64 }{
				{box.LatMax, lo + frac*(hi-lo)},
				{box.LatMin, lo + frac*(hi-lo)},
				{box.LatMin + frac*(box.LatMax-box.LatMin), hi},
				{box.LatMin + frac*(box.LatMax-box.LatMin), lo},
			}
			for _, e := range edge {
				if e.lat == 90 || e.lat == -90 {
					continue // the pole, where the box is clamped
				}
				d := geo.Haversine(lat, 0, e.lat, e.lng)
				assert.GreaterOrEqual(t, d, entity.NearbyThresholdKm, "lat %.1f, edge point (%.3f, %.3f)", lat, e.lat, e.lng)
			}
		}
	}
}

func TestGeoLocation_Box(t *testing.T) {
	t.Parallel()

	t.Run("no point, no box", func(t *testing.T) {
		_, ok := (&entity.GeoLocation{AdminArea: "HK"}).Box()
		assert.False(t, ok)
	})
	t.Run("a box across the antimeridian is two longitude ranges", func(t *testing.T) {
		suva := &entity.GeoLocation{AdminArea: "FJ-C", Point: &entity.Coordinates{Latitude: -18.14, Longitude: 178.44}}
		box, ok := suva.Box()
		require.True(t, ok)
		require.Len(t, box.Lng, 2)
		assert.True(t, box.Contains(entity.Coordinates{Latitude: -17.8, Longitude: -179.9}), "just across 180°")
		assert.True(t, box.Contains(entity.Coordinates{Latitude: -18.0, Longitude: 179.5}))
	})
	t.Run("near a pole the box takes every longitude", func(t *testing.T) {
		box, ok := (&entity.GeoLocation{AdminArea: "XX", Point: &entity.Coordinates{Latitude: 84, Longitude: 10}}).Box()
		require.True(t, ok)
		assert.Equal(t, [][2]float64{{-180, 180}}, box.Lng)
	})
}

func TestGeoLocation_MayBeNear(t *testing.T) {
	t.Parallel()

	point := &entity.GeoLocation{AdminArea: "JP-13", Point: &entity.Coordinates{Latitude: 35.681, Longitude: 139.767}}
	tokyo, hokkaido := "JP-13", "JP-01"

	assert.True(t, point.MayBeNear(&entity.Venue{AdminArea: &tokyo}), "the admin area without coordinates")
	assert.True(t, point.MayBeNear(&entity.Venue{AdminArea: &hokkaido, Coordinates: &entity.Coordinates{Latitude: 34.975, Longitude: 138.383}}), "inside the box")
	assert.False(t, point.MayBeNear(&entity.Venue{AdminArea: &hokkaido, Coordinates: &entity.Coordinates{Latitude: 43.062, Longitude: 141.354}}), "outside the box and the admin area")
	assert.False(t, point.MayBeNear(&entity.Venue{AdminArea: &hokkaido}), "another admin area without coordinates")
	assert.False(t, point.MayBeNear(nil), "no venue")

	noPoint := &entity.GeoLocation{AdminArea: "HK"}
	hk, mo := "HK", "MO"
	assert.True(t, noPoint.MayBeNear(&entity.Venue{AdminArea: &hk}))
	assert.False(t, noPoint.MayBeNear(&entity.Venue{AdminArea: &mo, Coordinates: &entity.Coordinates{Latitude: 22.19, Longitude: 113.54}}), "no point, so only the admin area")
}

func TestGeoLocation_Validate(t *testing.T) {
	t.Parallel()

	point := func(lat, lng float64) *entity.Coordinates { return &entity.Coordinates{Latitude: lat, Longitude: lng} }
	tests := []struct {
		name    string
		loc     entity.GeoLocation
		wantErr bool
	}{
		// @spec components/entity/geo-location "Latitude out of range"
		{name: "Latitude out of range", loc: entity.GeoLocation{AdminArea: "JP-13", Point: point(91, 139)}, wantErr: true},
		// @spec components/entity/geo-location "Longitude out of range"
		{name: "Longitude out of range", loc: entity.GeoLocation{AdminArea: "JP-13", Point: point(35, -181)}, wantErr: true},
		// @spec components/entity/geo-location "Empty admin area"
		{name: "Empty admin area", loc: entity.GeoLocation{Point: point(35, 139)}, wantErr: true},
		// @spec components/entity/geo-location "Free-text admin area"
		{name: "Free-text admin area", loc: entity.GeoLocation{AdminArea: "Tokyo", Point: point(35, 139)}, wantErr: true},
		// @spec components/entity/geo-location "Subdivision code with a point"
		{name: "Subdivision code with a point", loc: entity.GeoLocation{AdminArea: "JP-13", Point: point(35, 139)}},
		// @spec components/entity/geo-location "Territory code without a point"
		{name: "Territory code without a point", loc: entity.GeoLocation{AdminArea: "HK"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.loc.Validate()
			if tt.wantErr {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGeoLocation_AsHome(t *testing.T) {
	t.Parallel()

	tokyo := &entity.GeoLocation{AdminArea: "JP-13", Point: &entity.Coordinates{Latitude: 35.681, Longitude: 139.767}}
	concertAt := func(area string, lat, lng float64) *entity.Concert {
		return &entity.Concert{Venue: &entity.Venue{AdminArea: &area, Coordinates: &entity.Coordinates{Latitude: lat, Longitude: lng}}}
	}

	t.Run("Same admin area", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/geo-location "Same admin area"
		assert.Equal(t, entity.ProximityHome, concertAt("JP-13", 35.0, 139.0).ProximityTo(tokyo.AsHome()))
	})
	t.Run("Within 200 km of the point", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/geo-location "Within 200 km of the point"
		// Utsunomiya, about 100 km away in JP-09.
		assert.Equal(t, entity.ProximityNearby, concertAt("JP-09", 36.555, 139.883).ProximityTo(tokyo.AsHome()))
	})
	t.Run("No point", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/geo-location "No point"
		hk := &entity.GeoLocation{AdminArea: "HK"}
		// Shenzhen, about 30 km away.
		assert.Equal(t, entity.ProximityAway, concertAt("CN-GD", 22.543, 114.058).ProximityTo(hk.AsHome()))
	})
}
