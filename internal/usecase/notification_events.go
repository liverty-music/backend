package usecase

// ConcertCreatedData is the input for the NotifyNewConcerts use case.
// It carries only the identifiers of concerts that were just created so the
// notification pipeline computes filters and payloads exclusively from the
// new-concert set.
type ConcertCreatedData struct {
	// ArtistID is the internal UUID of the artist whose followers may be notified.
	ArtistID string `json:"artist_id"`
	// ConcertIDs contains the identifiers of the newly created concerts.
	ConcertIDs []string `json:"concert_ids"`
}
