package entity

import (
	"context"
	"time"
)

// RejectedConcertLog is an append-only record of a staged concert that was
// rejected by a developer during admin review.
//
// The log is used exclusively for search-quality analysis and is never
// consulted by the discovery dedup path, so a rejected concert can always
// re-enter the staging queue on a subsequent discovery run.
//
// ArtistID intentionally carries no foreign key so historical records survive
// artist deletion.
type RejectedConcertLog struct {
	// ID is the primary key (UUIDv7, application-generated).
	ID string
	// ArtistID is the internal UUID of the performing artist at rejection time.
	// No FK constraint — records must survive artist deletion.
	ArtistID string
	// ArtistName is the display name of the artist captured at rejection time.
	ArtistName string
	// Title is the descriptive title of the rejected concert.
	Title string
	// LocalDate is the scheduled calendar date of the rejected concert.
	LocalDate time.Time
	// StartTime is the scheduled start time. Nil when unknown.
	StartTime *time.Time
	// OpenTime is the doors-open time. Nil when not announced.
	OpenTime *time.Time
	// ListedVenueName is the raw scraped venue name of the rejected concert.
	ListedVenueName string
	// AdminArea is the administrative area extracted for the rejected concert.
	// Nil when not extracted.
	AdminArea *string
	// SourceURL is the source URL of the rejected concert. Nil when not
	// provided.
	SourceURL *string
	// ResolvedPlaceID is the Google Places place id of the resolved venue at
	// rejection time. Nil when unresolved.
	ResolvedPlaceID *string
	// ResolvedVenueName is the resolved canonical venue name at rejection time.
	// Nil when unresolved.
	ResolvedVenueName *string
	// ResolvedAdminArea is the resolved admin area at rejection time. Nil when
	// unresolved.
	ResolvedAdminArea *string
	// Reason is the reviewer-provided reason for rejecting the concert.
	Reason string
	// ReviewedBy is the Zitadel subject (identity) of the developer who
	// rejected the concert. Nil when unavailable.
	ReviewedBy *string
	// RejectedTime is the timestamp when the concert was rejected.
	RejectedTime time.Time
}

// RejectedConcertLogRepository defines the append-only data access interface
// for the rejected concerts log.
type RejectedConcertLogRepository interface {
	// Append inserts a new rejection log entry. It is append-only; no update
	// or delete operations are defined on this table.
	//
	// # Possible errors
	//
	//  - Internal: unexpected failure.
	Append(ctx context.Context, log *RejectedConcertLog) error
}
