package entity

import (
	"context"
	"time"
)

// ConcertSearchStatus represents the state of a concert search job.
type ConcertSearchStatus string

const (
	// ConcertSearchStatusPending indicates a search is currently in progress.
	ConcertSearchStatusPending ConcertSearchStatus = "pending"
	// ConcertSearchStatusCompleted indicates the search completed successfully.
	ConcertSearchStatusCompleted ConcertSearchStatus = "completed"
	// ConcertSearchStatusFailed indicates the search failed after all retries.
	ConcertSearchStatusFailed ConcertSearchStatus = "failed"
)

// ConcertSearch represents a record of when an artist's concerts were last searched
// via an external source (e.g., Gemini API).
type ConcertSearch struct {
	// ArtistID is the ID of the artist that was searched.
	ArtistID string
	// SearchTime is the timestamp of the most recent search.
	SearchTime time.Time
	// Status is the current state of the search job.
	Status ConcertSearchStatus
	// LastFoundTime is the timestamp of the most recent search that discovered
	// at least one new concert. Zero means no discovery has ever been recorded.
	LastFoundTime time.Time
}

// IsFresh reports whether this search log represents a recently completed search
// that is still within the given TTL. Returns false if the status is not Completed.
func (sl *ConcertSearch) IsFresh(now time.Time, ttl time.Duration) bool {
	return sl.Status == ConcertSearchStatusCompleted && now.Sub(sl.SearchTime) < ttl
}

// IsPending reports whether this search log represents an in-progress search
// that has not yet exceeded the given timeout. Returns false if the status is not Pending.
func (sl *ConcertSearch) IsPending(now time.Time, timeout time.Duration) bool {
	return sl.Status == ConcertSearchStatusPending && now.Sub(sl.SearchTime) < timeout
}

// WasRecentlyDiscovered reports whether a new concert was discovered for this
// artist within the given window. Returns false when no discovery has ever been
// recorded (LastFoundTime is zero), so an artist that never yielded a concert is
// never skipped on this basis.
func (sl *ConcertSearch) WasRecentlyDiscovered(now time.Time, window time.Duration) bool {
	if sl.LastFoundTime.IsZero() {
		return false
	}
	return now.Sub(sl.LastFoundTime) < window
}

// ConcertSearchRepository defines the data access interface for search logs.
type ConcertSearchRepository interface {
	// GetByArtistID retrieves the search log for a specific artist.
	//
	// # Possible errors
	//
	//  - NotFound: If no search log exists for the artist.
	GetByArtistID(ctx context.Context, artistID string) (*ConcertSearch, error)

	// Upsert creates or updates the search log for an artist with the given status.
	//
	// # Possible errors
	//
	//  - Internal: If the upsert fails.
	Upsert(ctx context.Context, artistID string, status ConcertSearchStatus) error

	// UpdateStatus updates the status for an existing search log.
	//
	// # Possible errors
	//
	//  - Internal: If the update fails.
	UpdateStatus(ctx context.Context, artistID string, status ConcertSearchStatus) error

	// MarkFound records that a search discovered at least one new concert by
	// setting the artist's last_found_at to the current time. It assumes the
	// row already exists (created by the pending Upsert before the search).
	//
	// # Possible errors
	//
	//  - Internal: If the update fails.
	MarkFound(ctx context.Context, artistID string) error

	// Delete removes the search log for a specific artist.
	//
	// # Possible errors
	//
	//  - Internal: If the delete fails.
	Delete(ctx context.Context, artistID string) error
}
