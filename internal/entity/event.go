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
	// CreateTime is the timestamp when the event was created.
	CreateTime time.Time
	// UpdateTime is the timestamp when the event was last updated.
	UpdateTime time.Time
}
