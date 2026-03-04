package usecase

// SearchStatusValue represents the polling-facing search status for a single artist.
// It is derived from entity.SearchLog but includes additional states (e.g., Unspecified
// for artists without a search log entry) and stale-pending detection.
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
