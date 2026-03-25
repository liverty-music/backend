package entity

// Event subject constants for domain events published via messaging.
const (
	SubjectConcertDiscovered = "CONCERT.discovered"
	SubjectConcertCreated    = "CONCERT.created"
	SubjectArtistCreated     = "ARTIST.created"
	SubjectUserCreated       = "USER.created"
)

// ConcertDiscoveredData is the payload for concert.discovered.v1 events.
// It carries the full batch of scraped concerts for one artist (post-deduplication).
// Published by SearchNewConcerts after external API call and dedup.
type ConcertDiscoveredData struct {
	// ArtistID is the internal UUID of the artist.
	ArtistID string `json:"artist_id"`
	// ArtistName is the display name of the artist (for notification context).
	ArtistName string `json:"artist_name"`
	// Concerts is the list of newly discovered, deduplicated scraped concerts.
	Concerts ScrapedConcerts `json:"concerts"`
}

// ConcertCreatedData is the payload for concert.created.v1 events.
// Published by the create-concerts consumer after persisting concerts.
type ConcertCreatedData struct {
	// ArtistID is the internal UUID of the artist.
	ArtistID string `json:"artist_id"`
	// ArtistName is the display name of the artist (for notification context).
	ArtistName string `json:"artist_name"`
	// ConcertCount is the number of concerts created in this batch.
	ConcertCount int `json:"concert_count"`
}

// UserCreatedData is the payload for user.created events.
// Published by UserUseCase.Create after persisting a new user.
type UserCreatedData struct {
	// ExternalID is the Zitadel user ID (JWT sub claim).
	ExternalID string `json:"external_id"`
	// Email is the user's email address.
	Email string `json:"email"`
}

// ArtistCreatedData is the payload for artist.created events.
// Published by persistArtists when new artists are inserted into the database.
type ArtistCreatedData struct {
	// ArtistID is the internal UUID of the artist.
	ArtistID string `json:"artist_id"`
	// ArtistName is the display name of the artist.
	ArtistName string `json:"artist_name"`
	// MBID is the MusicBrainz identifier for canonical identity.
	MBID string `json:"mbid"`
}
