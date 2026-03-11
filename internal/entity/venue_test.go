package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestNewVenueFromScraped(t *testing.T) {
	t.Parallel()

	t.Run("set non-empty ID, Name, RawName, and EnrichmentStatusPending", func(t *testing.T) {
		t.Parallel()

		got := entity.NewVenueFromScraped("Budokan")

		assert.NotEmpty(t, got.ID)
		assert.Equal(t, "Budokan", got.Name)
		assert.Equal(t, "Budokan", got.RawName)
		assert.Equal(t, entity.EnrichmentStatusPending, got.EnrichmentStatus)
	})

	t.Run("set nil optional fields", func(t *testing.T) {
		t.Parallel()

		got := entity.NewVenueFromScraped("Zepp Tokyo")

		assert.Nil(t, got.AdminArea)
		assert.Nil(t, got.MBID)
		assert.Nil(t, got.GooglePlaceID)
		assert.Nil(t, got.Coordinates)
	})

	t.Run("generate different IDs on successive calls", func(t *testing.T) {
		t.Parallel()

		a := entity.NewVenueFromScraped("Budokan")
		b := entity.NewVenueFromScraped("Budokan")

		assert.NotEqual(t, a.ID, b.ID)
	})
}
