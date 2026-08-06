package entity

// GeoLocation is a geographic reference point used for proximity-based concert
// queries. It is semantically neutral: unlike [Home], it carries no assumption
// about whose location the coordinates represent. Latitude/Longitude drive the
// Haversine distance (NEARBY tier); AdminArea drives the exact venue admin_area
// match (HOME tier).
//
// Corresponds to liverty_music.entity.v1.GeoLocation.
type GeoLocation struct {
	// Latitude is the WGS 84 latitude in decimal degrees of the reference point.
	Latitude float64
	// Longitude is the WGS 84 longitude in decimal degrees of the reference point.
	Longitude float64
	// AdminArea is the ISO 3166-2 subdivision code (e.g., "JP-13") used for
	// HOME-tier classification against a venue's admin_area.
	AdminArea string
}

// AsHome adapts this GeoLocation into a transient [Home] so it can be passed to
// the shared proximity classifier [GroupByDateAndProximity] / [Concert.ProximityTo],
// which reference a Home. The returned Home is not a user record — it exists only
// to carry the reference point's Level1 (admin_area) and Centroid (coordinates)
// for one classification pass.
func (g *GeoLocation) AsHome() *Home {
	return &Home{
		Level1:   g.AdminArea,
		Centroid: &Coordinates{Latitude: g.Latitude, Longitude: g.Longitude},
	}
}
