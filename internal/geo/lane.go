package geo

// Lane represents the geographic relationship between a user's home and a
// concert venue.
type Lane string

const (
	// LaneHome means the venue is in the user's home administrative area.
	LaneHome Lane = "HOME"
	// LaneNearby means the venue is within the nearby distance threshold from
	// the user's home centroid.
	LaneNearby Lane = "NEARBY"
	// LaneAway means the venue is beyond the nearby threshold or coordinates
	// are unavailable.
	LaneAway Lane = "AWAY"
)

// NearbyThresholdKm is the maximum great-circle distance (in kilometres) from
// the user's home centroid for a venue to be classified as NEARBY.
const NearbyThresholdKm = 200.0

// ClassifyLane determines the geographic lane for a concert based on the
// user's home prefecture and the venue's location.
//
// Classification rules (evaluated in order):
//  1. HOME — venue admin_area matches the user's home (ISO 3166-2 code).
//  2. NEARBY — Haversine distance from user home centroid to venue ≤ 200 km.
//  3. AWAY — everything else, including cases where coordinates are missing.
func ClassifyLane(homeLevel1 string, venueLat, venueLng *float64, venueAdminArea *string) Lane {
	// HOME: admin_area match takes priority.
	if venueAdminArea != nil && *venueAdminArea == homeLevel1 {
		return LaneHome
	}

	// NEARBY: requires both user home centroid and venue coordinates.
	if venueLat == nil || venueLng == nil {
		return LaneAway
	}
	centroid, ok := PrefectureCentroid(homeLevel1)
	if !ok {
		return LaneAway
	}

	dist := Haversine(centroid.Lat, centroid.Lng, *venueLat, *venueLng)
	if dist <= NearbyThresholdKm {
		return LaneNearby
	}
	return LaneAway
}
