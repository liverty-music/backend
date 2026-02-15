package entity

import (
	"context"
	"time"
)

// SearchLog represents a record of when an artist's concerts were last searched
// via an external source (e.g., Gemini API).
type SearchLog struct {
	// ArtistID is the ID of the artist that was searched.
	ArtistID string
	// SearchTime is the timestamp of the most recent search.
	SearchTime time.Time
}

// SearchLogRepository defines the data access interface for search logs.
type SearchLogRepository interface {
	// GetByArtistID retrieves the search log for a specific artist.
	//
	// # Possible errors
	//
	//  - NotFound: If no search log exists for the artist.
	GetByArtistID(ctx context.Context, artistID string) (*SearchLog, error)

	// Upsert creates or updates the search log for an artist with the current timestamp.
	//
	// # Possible errors
	//
	//  - Internal: If the upsert fails.
	Upsert(ctx context.Context, artistID string) error
}
