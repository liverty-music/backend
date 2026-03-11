package entity

// Proximity represents the geographic relationship between a user's home area
// and a concert venue.
//
// Corresponds to liverty_music.entity.v1.Proximity.
type Proximity string

const (
	// ProximityHome means the venue is in the user's home administrative area.
	ProximityHome Proximity = "HOME"
	// ProximityNearby means the venue is within the nearby distance threshold
	// from the user's home centroid.
	ProximityNearby Proximity = "NEARBY"
	// ProximityAway means the venue is beyond the nearby threshold, has unknown
	// location, or the user has no home set.
	ProximityAway Proximity = "AWAY"
)

// NearbyThresholdKm is the maximum great-circle distance (in kilometres) from
// the user's home centroid for a venue to be classified as NEARBY.
const NearbyThresholdKm = 200.0
