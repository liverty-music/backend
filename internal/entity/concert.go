package entity

import (
	"context"
	"time"
)

// Concert represents a live music event domain entity.
type Concert struct {
	ID           string
	Title        string
	ArtistID     string
	VenueName    string
	VenueCity    string
	VenueCountry string
	VenueAddress string
	EventDate    time.Time
	TicketURL    string
	Price        float64
	Currency     string
	Status       ConcertStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ConcertStatus represents the status of a concert.
type ConcertStatus string

const (
	ConcertStatusScheduled ConcertStatus = "scheduled"
	ConcertStatusSoldOut   ConcertStatus = "sold_out"
	ConcertStatusCancelled ConcertStatus = "cancelled"
	ConcertStatusCompleted ConcertStatus = "completed"
)

// NewConcert represents data for creating a new concert.
type NewConcert struct {
	Title        string
	ArtistID     string
	VenueName    string
	VenueCity    string
	VenueCountry string
	VenueAddress string
	EventDate    time.Time
	TicketURL    string
	Price        float64
	Currency     string
	Status       ConcertStatus
}

// ConcertRepository defines the interface for concert data access.
type ConcertRepository interface {
	Create(ctx context.Context, params *NewConcert) (*Concert, error)
	Get(ctx context.Context, id string) (*Concert, error)
	GetByArtist(ctx context.Context, artistID string, limit, offset int) ([]*Concert, error)
	GetByLocation(ctx context.Context, city, country string, limit, offset int) ([]*Concert, error)
	GetUpcoming(ctx context.Context, limit, offset int) ([]*Concert, error)
	Update(ctx context.Context, id string, params *NewConcert) (*Concert, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, limit, offset int) ([]*Concert, error)
}