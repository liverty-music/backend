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

// IsFresh reports whether this search log represents a recently completed search
// that is still within the given TTL. Returns false if the status is not Completed.
func (sl *SearchLog) IsFresh(now time.Time, ttl time.Duration) bool {
	return sl.Status == SearchLogStatusCompleted && now.Sub(sl.SearchTime) < ttl
}

// IsPending reports whether this search log represents an in-progress search
// that has not yet exceeded the given timeout. Returns false if the status is not Pending.
func (sl *SearchLog) IsPending(now time.Time, timeout time.Duration) bool {
	return sl.Status == SearchLogStatusPending && now.Sub(sl.SearchTime) < timeout
}

// SearchLogRepository defines the data access interface for search logs.
type SearchLogRepository interface {
	// GetByArtistID retrieves the search log for a specific artist.
	//
	// # Possible errors
	//
	//  - NotFound: If no search log exists for the artist.
	GetByArtistID(ctx context.Context, artistID string) (*SearchLog, error)

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
