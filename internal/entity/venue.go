package entity

import (
	"context"
	"time"
)

// Venue represents a physical location where events are hosted.
//
// Corresponds to liverty_music.entity.v1.Venue.
type Venue struct {
	// ID is the unique identifier for the venue (UUID).
	ID string
	// Name is the name of the venue.
	Name string
	// CreateTime is the timestamp when the venue was created.
	CreateTime time.Time
	// UpdateTime is the timestamp when the venue was last updated.
	UpdateTime time.Time
}

// NewVenue represents data for creating a new venue.
type NewVenue struct {
	// Name is the name of the venue to create.
	Name string
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

	// GetByName retrieves a venue by Name.
	//
	// # Possible errors
	//
	//  - NotFound: If the venue does not exist.
	GetByName(ctx context.Context, name string) (*Venue, error)
}
