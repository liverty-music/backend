package geo

import (
	"fmt"

	"github.com/liverty-music/backend/internal/entity"
)

// CentroidResolverImpl implements usecase.CentroidResolver using the static
// centroid lookup table.
type CentroidResolverImpl struct{}

// NewCentroidResolver returns a CentroidResolverImpl.
func NewCentroidResolver() *CentroidResolverImpl {
	return &CentroidResolverImpl{}
}

// ResolveCentroid returns the geographic centroid for the home's Level1
// ISO 3166-2 subdivision code. Returns an error if the code is not found.
func (r *CentroidResolverImpl) ResolveCentroid(home *entity.Home) (lat, lng float64, err error) {
	if home == nil {
		return 0, 0, fmt.Errorf("home is nil")
	}
	c, ok := ResolveCentroid(home.Level1)
	if !ok {
		return 0, 0, fmt.Errorf("centroid not found for level1 code %q", home.Level1)
	}
	return c.Latitude, c.Longitude, nil
}
