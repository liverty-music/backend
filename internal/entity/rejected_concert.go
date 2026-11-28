package entity

import (
	"context"
	"slices"
	"time"
)

// RejectedConcert is an append-only record of a concert candidate that was
// rejected by a developer during admin review.
//
// The log serves search-quality analysis and keeps discovery from staging a
// listing an admin already settled ([RejectedConcert.SameListing]); a
// corrected listing is staged again.
//
// ArtistID intentionally carries no foreign key so historical records survive
// artist deletion.
type RejectedConcert struct {
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
	// StartTime is the wall-clock start time at the venue, read in TimeZone.
	// Nil when unknown.
	StartTime *TimeOfDay
	// OpenTime is the wall-clock doors-open time, read like StartTime. Nil when
	// not announced.
	OpenTime *TimeOfDay
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
	// TimeZone is the venue's time zone as discovery resolved it, kept from
	// the candidate. Nil when it had none.
	TimeZone *string
	// Reason is the reviewer-provided reason for rejecting the concert.
	Reason string
	// ReviewedBy is the Zitadel subject (identity) of the developer who
	// rejected the concert. Nil when unavailable.
	ReviewedBy *string
	// RejectTime is the timestamp when the concert was rejected.
	RejectTime time.Time
}

// RejectedConcertRepository defines the append-only data access interface
// for the rejected concerts log.
type RejectedConcertRepository interface {
	// Append inserts a new rejection log entry. It is append-only; no update
	// or delete operations are defined on this table.
	//
	// # Possible errors
	//
	//  - Internal: unexpected failure.
	Append(ctx context.Context, log *RejectedConcert) error

	// ListByArtist returns the Artist's entries with the fields
	// [RejectedConcert.SameListing] reads (ID, ArtistID, LocalDate,
	// ListedVenueName, StartTime) and no others, oldest first, and an empty
	// list when there is none.
	//
	// # Possible errors
	//
	//   - InvalidArgument: artistID is empty.
	//   - Unavailable: the store cannot be reached.
	//   - Internal: unexpected failure.
	ListByArtist(ctx context.Context, artistID string) ([]*RejectedConcert, error)
}

// SameListing reports whether ev, found for the Artist artistID, is the same
// listing as the entry: the entry's Artist, local date and listed venue (see
// [SameListedVenue]) and the same start time, two unknown start times counting
// as the same (RejectedConcert spec, "Same listing").
func (r *RejectedConcert) SameListing(artistID string, ev *DiscoveredEvent) bool {
	return artistID == r.ArtistID &&
		DateVenueKey(ev.LocalDate, ev.ListedVenueName) == DateVenueKey(r.LocalDate, r.ListedVenueName) &&
		StartKey(ev.StartTime) == StartKey(r.StartTime)
}

// DropRejected removes from series, found for the Artist artistID, the events
// that are the same listing as one of the entries ([RejectedConcert.SameListing]).
// A series left with no events is removed entirely. It reuses the events
// slices of series.
func DropRejected(artistID string, series []*DiscoveredSeries, rejected []*RejectedConcert) []*DiscoveredSeries {
	if len(rejected) == 0 {
		return series
	}
	kept := series[:0]
	for _, s := range series {
		events := s.Events[:0]
		for _, ev := range s.Events {
			if !slices.ContainsFunc(rejected, func(r *RejectedConcert) bool { return r.SameListing(artistID, ev) }) {
				events = append(events, ev)
			}
		}
		if len(events) == 0 {
			continue
		}
		s.Events = events
		kept = append(kept, s)
	}
	return kept
}
