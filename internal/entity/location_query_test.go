package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
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
