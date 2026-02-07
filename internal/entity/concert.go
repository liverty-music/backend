package entity

import (
	"context"
	"time"
)

// Concert represents a specific music live event.
//
// Corresponds to liverty_music.entity.v1.Concert.
type Concert struct {
	Event
	// ArtistID is the ID of the artist performing.
	ArtistID string
}

// ScrapedConcert represents raw concert information rediscovered from external sources.
// It lacks system-specific identifiers like ID or ArtistID.
type ScrapedConcert struct {
	// Title is the descriptive title of the scraped event.
	Title          string
	// VenueName is the raw name of the venue from the source.
	VenueName      string
	// LocalEventDate represents the calendar date of the event.
	// See entity.Concert.LocalEventDate for detailed specifications.
	LocalEventDate time.Time
	// StartTime is the specific starting time (optional).
	StartTime      *time.Time
	// OpenTime is the time when doors open (optional).
	OpenTime       *time.Time
	// SourceURL is the URL where this information was found.
	SourceURL      string
}


// ConcertRepository defines the data access interface for Concerts.
type ConcertRepository interface {
	// ListByArtist retrieves all concerts for a specific artist.
	// if upcomingOnly is true, it only returns concerts with LocalEventDate >= today.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist ID is empty.
	ListByArtist(ctx context.Context, artistID string, upcomingOnly bool) ([]*Concert, error)
	// Create creates a new concert.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If required fields are missing.
	//  - AlreadyExists: If a concert with the same unique key already exists.
	Create(ctx context.Context, concert *Concert) error
}

// ConcertSearcher defines the interface for searching concerts from external sources.
type ConcertSearcher interface {
	// Search uses an external service (e.g., Gemini) to find concerts for an artist.
	// It relies on the artist's name and official site URL for grounding.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist or official site is invalid.
	//  - Unavailable: If the external service is down.
	Search(ctx context.Context, artist *Artist, officialSite *OfficialSite, from time.Time) ([]*ScrapedConcert, error)
}
