package entity

// Coordinates represents a WGS 84 geographic point.
//
// Corresponds to liverty_music.entity.v1.Coordinates.
type Coordinates struct {
	// Latitude is the WGS 84 latitude in decimal degrees.
	Latitude float64
	// Longitude is the WGS 84 longitude in decimal degrees.
	Longitude float64
}
