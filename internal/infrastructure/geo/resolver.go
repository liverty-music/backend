package geo

import (
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// CentroidResolverImpl implements usecase.CentroidResolver using the static
// centroid lookup table.
type CentroidResolverImpl struct{}

// NewCentroidResolver returns a CentroidResolverImpl.
func NewCentroidResolver() *CentroidResolverImpl {
	return &CentroidResolverImpl{}
}

// ResolveCentroid returns the geographic centroid for the home's Level1
// ISO 3166-2 subdivision code.
//
// # Possible errors
//
//   - InvalidArgument: no home is given.
//   - NotFound: the lookup table has no centroid for the home's level-1 code.
func (r *CentroidResolverImpl) ResolveCentroid(home *entity.Home) (lat, lng float64, err error) {
	if home == nil {
		return 0, 0, apperr.New(codes.InvalidArgument, "a home is required to resolve its centroid")
	}
	c, ok := ResolveCentroid(home.Level1)
	if !ok {
		return 0, 0, apperr.New(codes.NotFound, "no centroid is known for the home's level-1 code",
			slog.String("level_1", home.Level1))
	}
	return c.Latitude, c.Longitude, nil
}
