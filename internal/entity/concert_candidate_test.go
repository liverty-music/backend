package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
)

func TestConcertCandidate_VenueRules(t *testing.T) {
	t.Parallel()

	str := func(s string) *string { return &s }
	lat, lng := 35.55, 139.75

	t.Run("admin area prefers the resolved one", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, str("JP-13"), (&entity.ConcertCandidate{AdminArea: str("JP-14"), ResolvedAdminArea: str("JP-13")}).VenueAdminArea())
		assert.Equal(t, str("JP-14"), (&entity.ConcertCandidate{AdminArea: str("JP-14")}).VenueAdminArea(), "the scraped one when unresolved")
		assert.Nil(t, (&entity.ConcertCandidate{}).VenueAdminArea())
	})

	t.Run("place id backfill", func(t *testing.T) {
		t.Parallel()
		withPlace := &entity.ConcertCandidate{ResolvedPlaceID: str("p-1")}
		assert.True(t, withPlace.ShouldBackfillPlaceID(&entity.Venue{}), "a venue without a place id takes the candidate's")
		assert.False(t, withPlace.ShouldBackfillPlaceID(&entity.Venue{GooglePlaceID: str("p-0")}), "a stored place id is never overwritten")
		assert.False(t, (&entity.ConcertCandidate{}).ShouldBackfillPlaceID(&entity.Venue{}), "an unresolved candidate has none to give")
	})

	t.Run("new venue from a resolved candidate", func(t *testing.T) {
		t.Parallel()
		v := (&entity.ConcertCandidate{
			ListedVenueName: "ゼップ羽田", ResolvedVenueName: str("Zepp Haneda"), ResolvedPlaceID: str("p-1"),
			AdminArea: str("JP-14"), ResolvedAdminArea: str("JP-13"), ResolvedLatitude: &lat, ResolvedLongitude: &lng,
		}).NewVenue()

		assert.NotEmpty(t, v.ID)
		assert.Equal(t, "Zepp Haneda", v.Name, "the canonical name")
		assert.Equal(t, str("ゼップ羽田"), v.ListedVenueName)
		assert.Equal(t, str("JP-13"), v.AdminArea)
		assert.Equal(t, str("p-1"), v.GooglePlaceID)
		assert.Equal(t, &entity.Coordinates{Latitude: lat, Longitude: lng}, v.Coordinates)
	})

	t.Run("new venue from an unresolved candidate", func(t *testing.T) {
		t.Parallel()
		v := (&entity.ConcertCandidate{ListedVenueName: "ゼップ羽田", ResolvedLatitude: &lat}).NewVenue()

		assert.Equal(t, "ゼップ羽田", v.Name, "the listed name when unresolved")
		assert.Nil(t, v.GooglePlaceID)
		assert.Nil(t, v.Coordinates, "coordinates need both")
	})
}
