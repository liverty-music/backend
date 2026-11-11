package geo_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCentroidResolverImpl_ResolveCentroid(t *testing.T) {
	t.Parallel()

	resolver := geo.NewCentroidResolver()

	t.Run("return the centroid of a known level-1 code", func(t *testing.T) {
		t.Parallel()

		lat, lng, err := resolver.ResolveCentroid(&entity.Home{CountryCode: "JP", Level1: "JP-13"})

		require.NoError(t, err)
		assert.InDelta(t, 35.6894, lat, 0.01)
		assert.InDelta(t, 139.6917, lng, 0.01)
	})

	t.Run("fail with InvalidArgument when no home is given", func(t *testing.T) {
		t.Parallel()

		_, _, err := resolver.ResolveCentroid(nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("fail with NotFound for an unknown level-1 code", func(t *testing.T) {
		t.Parallel()

		_, _, err := resolver.ResolveCentroid(&entity.Home{CountryCode: "US", Level1: "US-CA"})

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
