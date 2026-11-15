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
	query := entity.LocationQuery{Location: &entity.GeoLocation{Latitude: 35.6762, Longitude: 139.6503, AdminArea: tokyo}}

	groups := query.NearbyGroups([]*entity.Concert{home, nearby, awaySameDay, awayOnly})

	require.Len(t, groups, 1, "a date with only away concerts is dropped")
	assert.Equal(t, []*entity.Concert{home}, groups[0].Home)
	assert.Equal(t, []*entity.Concert{nearby}, groups[0].Nearby)
	assert.Empty(t, groups[0].Away, "away concerts are never returned")
}

// TestGeoLocation_BoxHoldsEveryNearbyPoint checks the box margins: from any
// reference point in Japan (latitudes 24°N to 46°N), every point on the box's
// edge is at least NearbyThresholdKm away, so no NEARBY venue lies outside it.
func TestGeoLocation_BoxHoldsEveryNearbyPoint(t *testing.T) {
	t.Parallel()

	for lat := 24.0; lat <= 46.0; lat += 0.5 {
		point := &entity.GeoLocation{Latitude: lat, Longitude: 139.0}
		box := point.Box()
		for i := 0; i <= 100; i++ {
			frac := float64(i) / 100
			edge := []struct{ lat, lng float64 }{
				{box.LatMax, box.LngMin + frac*(box.LngMax-box.LngMin)},
				{box.LatMin, box.LngMin + frac*(box.LngMax-box.LngMin)},
				{box.LatMin + frac*(box.LatMax-box.LatMin), box.LngMax},
				{box.LatMin + frac*(box.LatMax-box.LatMin), box.LngMin},
			}
			for _, e := range edge {
				d := geo.Haversine(lat, point.Longitude, e.lat, e.lng)
				assert.GreaterOrEqual(t, d, entity.NearbyThresholdKm, "lat %.1f, edge point (%.3f, %.3f)", lat, e.lat, e.lng)
			}
		}
	}
}

func TestGeoLocation_MayBeNear(t *testing.T) {
	t.Parallel()

	point := &entity.GeoLocation{Latitude: 35.681, Longitude: 139.767, AdminArea: "JP-13"}
	tokyo, hokkaido := "JP-13", "JP-01"

	assert.True(t, point.MayBeNear(&entity.Venue{AdminArea: &tokyo}), "the admin area without coordinates")
	assert.True(t, point.MayBeNear(&entity.Venue{AdminArea: &hokkaido, Coordinates: &entity.Coordinates{Latitude: 34.975, Longitude: 138.383}}), "inside the box")
	assert.False(t, point.MayBeNear(&entity.Venue{AdminArea: &hokkaido, Coordinates: &entity.Coordinates{Latitude: 43.062, Longitude: 141.354}}), "outside the box and the admin area")
	assert.False(t, point.MayBeNear(&entity.Venue{AdminArea: &hokkaido}), "another admin area without coordinates")
	assert.False(t, point.MayBeNear(nil), "no venue")
}
