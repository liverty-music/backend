package entity_test

import (
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
)

func TestConcertCandidate_Validate(t *testing.T) {
	t.Parallel()

	t.Run("Discovered area and time zone", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/concert-candidate "Discovered area and time zone"
		c := &entity.ConcertCandidate{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo")}
		assert.NoError(t, c.Validate())
	})
	t.Run("Area kept as discovered", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/concert-candidate "Area kept as discovered"
		c := &entity.ConcertCandidate{AdminArea: new("FR-33")}
		assert.NoError(t, c.Validate(), "a lower-level code is kept until approval")
		assert.Equal(t, "FR-33", *c.AdminArea)
	})
	t.Run("Time zone without an area", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/concert-candidate "Time zone without an area"
		c := &entity.ConcertCandidate{TimeZone: new("Asia/Tokyo")}
		assert.ErrorIs(t, c.Validate(), apperr.ErrInvalidArgument)
	})
}
