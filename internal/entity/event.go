package entity

import (
	"time"
)

// Event represents a generic event.
type Event struct {
	// ID is the unique identifier for the event (UUID).
	ID string
	// VenueID is the ID of the venue where the event takes place.
	VenueID string
	// Title is the descriptive title of the event.
	Title string
	// ListedVenueName is the raw venue name as listed in the source data.
	// It preserves the original scraped text separately from the normalized Venue.Name.
	ListedVenueName string
	// LocalEventDate represents the calendar date of the event in the local timezone.
	//
	// Specifications:
	// - Location MUST be set to time.UTC.
	// - Time components (Hour, Minute, Second, Nanosecond) MUST be zero (00:00:00).
	// This ensures that the date remains consistent when saved to a Postgres DATE type.
	// It avoids "date shifting" issues during timezone conversions.
	LocalEventDate time.Time
	// StartTime is the specific starting time of the event (optional).
	StartTime *time.Time
	// OpenTime is the time when doors open (optional).
	OpenTime *time.Time
	// SourceURL is the URL where this information was found.
	SourceURL string
}
