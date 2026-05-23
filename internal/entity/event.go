package entity

import (
	"time"
)

// Event represents a single performance occurring on a specific date at a specific venue.
//
// Every Event belongs to a parent [Series] that owns metadata shared across multiple
// events of the same engagement (tour title, source URL, classification). Series-level
// fields are intentionally absent from Event to avoid duplication when one series owns
// several events. Performing artists are modelled as an M:N relation via event_performers
// and surface on the [Concert] DTO as the Performers slice.
//
// See [EventProto] for the wire representation.
//
// [EventProto]: https://github.com/liverty-music/specification/blob/main/proto/liverty_music/entity/v1/event.proto
type Event struct {
	// ID is the unique identifier for the event (UUIDv7).
	ID string
	// SeriesID is the foreign key reference to the parent [Series].
	SeriesID string
	// VenueID is the ID of the venue where the event takes place.
	VenueID string
	// Venue is the resolved venue entity. Populated by the server on read operations.
	Venue *Venue
	// ListedVenueName is the raw venue name as listed in the source data.
	// It preserves the original scraped text separately from the normalized Venue.Name.
	// Nullable: legacy rows inserted before this field was added will have NULL.
	ListedVenueName *string
	// LocalDate represents the calendar date of the event in the local timezone.
	//
	// Specifications:
	// - Location MUST be set to time.UTC.
	// - Time components (Hour, Minute, Second, Nanosecond) MUST be zero (00:00:00).
	// This ensures that the date remains consistent when saved to a Postgres DATE type.
	// It avoids "date shifting" issues during timezone conversions.
	LocalDate time.Time
	// StartTime is the specific starting time of the event (optional).
	StartTime *time.Time
	// OpenTime is the time when doors open (optional).
	OpenTime *time.Time
}
