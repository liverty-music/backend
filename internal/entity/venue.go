package entity

import (
	"context"
)

// Venue represents a physical location where events are hosted.
//
// Corresponds to liverty_music.entity.v1.Venue.
type Venue struct {
	// ID is the unique identifier for the venue (UUID).
	ID string
	// Name is the canonical name of the venue (from Google Places API).
	Name string
	// AdminArea is the administrative area (prefecture, state, province) where the venue is located.
	// It is nil when the area could not be determined with confidence.
	AdminArea *string
	// GooglePlaceID is the Google Maps Place ID for the canonical venue record.
	GooglePlaceID *string
	// Coordinates is the WGS 84 geographic position of the venue.
	Coordinates *Coordinates
}

// VenuePlace represents a resolved canonical venue from an external place search service.
type VenuePlace struct {
	// ExternalID is the Google Place ID.
	ExternalID string
	// Name is the canonical name returned by the external service.
	Name string
	// Coordinates is the WGS 84 geographic position returned by the external service.
	// Nil when the external service did not provide coordinate data.
	Coordinates *Coordinates
}

// VenuePlaceSearcher defines the interface for external place search services used in venue resolution.
type VenuePlaceSearcher interface {
	// SearchPlace looks up a venue by name and optional administrative area.
	//
	// # Possible errors
	//
	//  - NotFound: If no matching place is found.
	//  - Unavailable: If the external service is unreachable.
	SearchPlace(ctx context.Context, name, adminArea string) (*VenuePlace, error)
}

// VenueRepository defines the data access interface for Venues.
type VenueRepository interface {
	// Create creates a new venue.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the venue name is invalid.
	Create(ctx context.Context, venue *Venue) error

	// Get retrieves a venue by ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the venue does not exist.
	Get(ctx context.Context, id string) (*Venue, error)

	// GetByPlaceID retrieves a venue by Google Maps Place ID.
	//
	// # Possible errors
	//
	//  - NotFound: If no venue with that place ID exists.
	GetByPlaceID(ctx context.Context, placeID string) (*Venue, error)
}
