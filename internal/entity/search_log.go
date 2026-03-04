package entity

import (
	"context"
	"time"
)

// SearchLogStatus represents the state of a concert search job.
type SearchLogStatus string

const (
	// SearchLogStatusPending indicates a search is currently in progress.
	SearchLogStatusPending SearchLogStatus = "pending"
	// SearchLogStatusCompleted indicates the search completed successfully.
	SearchLogStatusCompleted SearchLogStatus = "completed"
	// SearchLogStatusFailed indicates the search failed after all retries.
	SearchLogStatusFailed SearchLogStatus = "failed"
)

// SearchLog represents a record of when an artist's concerts were last searched
// via an external source (e.g., Gemini API).
type SearchLog struct {
	// ArtistID is the ID of the artist that was searched.
	ArtistID string
	// SearchTime is the timestamp of the most recent search.
	SearchTime time.Time
	// Status is the current state of the search job.
	Status SearchLogStatus
}

// SearchStatusValue represents the polling-facing search status for a single artist.
// It is derived from SearchLog but includes additional states (e.g., Unspecified for
// artists without a search log entry) and stale-pending detection.
type SearchStatusValue int

const (
	// SearchStatusUnspecified indicates no search log exists.
	SearchStatusUnspecified SearchStatusValue = iota
	// SearchStatusPending indicates a search is in progress.
	SearchStatusPending
	// SearchStatusCompleted indicates the search completed successfully.
	SearchStatusCompleted
	// SearchStatusFailed indicates the search failed.
	SearchStatusFailed
)

// SearchStatus holds the search status for a single artist.
type SearchStatus struct {
	ArtistID string
	Status   SearchStatusValue
}

// SearchLogRepository defines the data access interface for search logs.
type SearchLogRepository interface {
	// GetByArtistID retrieves the search log for a specific artist.
	//
	// # Possible errors
	//
	//  - NotFound: If no search log exists for the artist.
	GetByArtistID(ctx context.Context, artistID string) (*SearchLog, error)

	// ListByArtistIDs retrieves search logs for multiple artists.
	// Artists without a search log entry are omitted from the result.
	ListByArtistIDs(ctx context.Context, artistIDs []string) ([]*SearchLog, error)

	// Upsert creates or updates the search log for an artist with the given status.
	//
	// # Possible errors
	//
	//  - Internal: If the upsert fails.
	Upsert(ctx context.Context, artistID string, status SearchLogStatus) error

	// UpdateStatus updates the status for an existing search log.
	//
	// # Possible errors
	//
	//  - Internal: If the update fails.
	UpdateStatus(ctx context.Context, artistID string, status SearchLogStatus) error

	// Delete removes the search log for a specific artist.
	//
	// # Possible errors
	//
	//  - Internal: If the delete fails.
	Delete(ctx context.Context, artistID string) error
}
