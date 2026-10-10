package entity

import (
	"context"
	"time"
)

// Event represents a single occurrence on a specific date at a specific venue.
//
// Every Event belongs to a parent [Series] that owns metadata shared across multiple
// events of the same engagement (tour title, source URL, classification). Series-level
// fields are intentionally absent from Event to avoid duplication when one series owns
// several events. Event is generic: a kind of event embeds it and adds its own data,
// as [Concert] adds the performing artists (stored in concert_artists).
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

// EventRepository reads one [Event]'s current date and times.
// Implementations live in internal/infrastructure/database/rdb/.
type EventRepository interface {
	// Get returns the event's id, series, venue, current local date, open
	// time and start time, read afresh; an unknown time is nil. Venue is not
	// resolved.
	//
	// # Possible errors
	//
	//  - NotFound: no event has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id string) (*Event, error)
}
