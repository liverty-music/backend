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

// The margins, in degrees, of the box around a reference point that the
// location search reads first, before the precise distance cut. They are
// wide enough that the box holds every point within [NearbyThresholdKm] of a
// reference point in Japan (latitudes up to 46°N): 1.8° of latitude is about
// 200 km, and 2.6° of longitude is about 200 km at 46°N.
const (
	LocationBoxLatMargin = 1.8
	LocationBoxLngMargin = 2.6
)

// LocationBox is a latitude and longitude range, both ends included.
type LocationBox struct {
	LatMin, LatMax, LngMin, LngMax float64
}

// Box returns the box the location search reads first around the reference
// point: [LocationBoxLatMargin] and [LocationBoxLngMargin] on each side.
func (g *GeoLocation) Box() LocationBox {
	return LocationBox{
		LatMin: g.Latitude - LocationBoxLatMargin,
		LatMax: g.Latitude + LocationBoxLatMargin,
		LngMin: g.Longitude - LocationBoxLngMargin,
		LngMax: g.Longitude + LocationBoxLngMargin,
	}
}

// MayBeNear reports whether the location search reads a concert at venue v
// for this reference point: the venue is in the reference point's admin area,
// or its coordinates are in [GeoLocation.Box]. The search then classifies each
// concert by distance ([LocationQuery.NearbyGroups]); a venue that is not
// MayBeNear is never HOME or NEARBY.
func (g *GeoLocation) MayBeNear(v *Venue) bool {
	if v == nil {
		return false
	}
	if v.AdminArea != nil && *v.AdminArea == g.AdminArea {
		return true
	}
	if v.Coordinates == nil {
		return false
	}
	b := g.Box()
	lat, lng := v.Coordinates.Latitude, v.Coordinates.Longitude
	return lat >= b.LatMin && lat <= b.LatMax && lng >= b.LngMin && lng <= b.LngMax
}
