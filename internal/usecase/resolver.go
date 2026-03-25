package usecase

import "github.com/liverty-music/backend/internal/entity"

// CentroidResolver resolves the geographic centroid for a home location.
// Implementations map an ISO 3166-2 Level 1 subdivision code to approximate
// WGS 84 lat/lng coordinates.
type CentroidResolver interface {
	ResolveCentroid(home *entity.Home) (lat, lng float64, err error)
}
