package messaging

import "time"

// ConcertDiscoveredData is the payload for concert.discovered.v1 events.
// It carries the full batch of scraped concerts for one artist (post-deduplication).
// Published by SearchNewConcerts after Gemini API call and dedup.
type ConcertDiscoveredData struct {
	// ArtistID is the internal UUID of the artist.
	ArtistID string `json:"artist_id"`
	// ArtistName is the display name of the artist (for notification context).
	ArtistName string `json:"artist_name"`
	// Concerts is the list of newly discovered, deduplicated scraped concerts.
	Concerts []ScrapedConcertData `json:"concerts"`
}

// ScrapedConcertData represents a single scraped concert within a discovered batch.
type ScrapedConcertData struct {
	Title           string     `json:"title"`
	ListedVenueName string     `json:"listed_venue_name"`
	AdminArea       *string    `json:"admin_area,omitempty"`
	LocalDate       time.Time  `json:"local_date"`
	StartTime       *time.Time `json:"start_time,omitempty"`
	OpenTime        *time.Time `json:"open_time,omitempty"`
	SourceURL       string     `json:"source_url"`
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

// VenueCreatedData is the payload for venue.created.v1 events.
// Published by the create-concerts consumer when a new venue is created.
type VenueCreatedData struct {
	// VenueID is the internal UUID of the newly created venue.
	VenueID string `json:"venue_id"`
	// Name is the venue name.
	Name string `json:"name"`
	// AdminArea is the administrative area, if known.
	AdminArea *string `json:"admin_area,omitempty"`
}
