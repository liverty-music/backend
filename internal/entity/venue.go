package entity

import (
	"context"
	"time"
)

// Venue represents a physical location where events are hosted.
type Venue struct {
	ID        string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewVenue represents data for creating a new venue.
type NewVenue struct {
	Name string
}

// VenueRepository defines the data access interface for Venues.
type VenueRepository interface {
	Create(ctx context.Context, venue *Venue) error
	Get(ctx context.Context, id string) (*Venue, error)
}
