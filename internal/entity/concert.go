package entity

import (
	"context"
	"time"
)

// Concert represents a specific music live event.
type Concert struct {
	ID           string
	ArtistID     string
	VenueID      string
	Title        string
	VenueName    string
	VenueCity    string
	VenueCountry string
	VenueAddress string
	Date         time.Time
	EventDate    time.Time
	StartTime    time.Time
	OpenTime     *time.Time
	TicketURL    string
	Price        float64
	Currency     string
	Status       ConcertStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time

	// Optional relations
	Artist *Artist
	Venue  *Venue
}

// NewConcert represents data for creating a new concert.
type NewConcert struct {
	ArtistID     string
	VenueID      string
	Title        string
	VenueName    string
	VenueCity    string
	VenueCountry string
	VenueAddress string
	EventDate    time.Time
	StartTime    time.Time
	OpenTime     *time.Time
	TicketURL    string
	Price        float64
	Currency     string
	Status       ConcertStatus
}

// ConcertStatus represents the current state of a concert.
type ConcertStatus string

// Concert status values.
const (
	// ConcertStatusScheduled indicates the concert is scheduled to occur.
	ConcertStatusScheduled ConcertStatus = "scheduled"
	ConcertStatusCanceled  ConcertStatus = "canceled"
	ConcertStatusCompleted ConcertStatus = "completed"
)

// ConcertRepository defines the data access interface for Concerts.
type ConcertRepository interface {
	ListByArtist(ctx context.Context, artistID string) ([]*Concert, error)
	Create(ctx context.Context, concert *Concert) error // Included for completeness
}
