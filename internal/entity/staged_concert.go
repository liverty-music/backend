package entity

import (
	"context"
	"time"
)

// StagedConcert is a concert discovered by the Gemini pipeline that is held in
// a pending approval queue until a developer approves or rejects it via the
// admin console.
//
// Venue resolution (Google Places) runs at staging time: the resolved canonical
// fields (ResolvedVenueName, ResolvedPlaceID, etc.) are denormalised onto the
// row so reviewers can judge venue accuracy without extra lookups. The canonical
// venues row is created only on approval.
//
// A row lives in this table only while it is pending — both Approve and Reject
// delete the row, so a re-discovered concert can re-enter the queue after a
// rejection.
type StagedConcert struct {
	// ID is the primary key (UUIDv7, application-generated).
	ID string
	// ArtistID is the FK to artists.id of the performing artist.
	ArtistID string
	// Title is the descriptive title extracted for the concert.
	Title string
	// LocalDate is the scheduled calendar date of the concert in the venue
	// local timezone.
	LocalDate time.Time
	// StartTime is the scheduled start time. Nil when the source did not
	// state one.
	StartTime *time.Time
	// OpenTime is the doors-open time. Nil when not announced.
	OpenTime *time.Time
	// ListedVenueName is the raw venue name exactly as scraped from the source.
	ListedVenueName string
	// AdminArea is the administrative area extracted by Gemini. Nil when not
	// extracted.
	AdminArea *string
	// SourceURL is the source URL where the concert was found. Nil when not
	// provided.
	SourceURL *string
	// ResolvedPlaceID is the Google Places place id of the resolved venue.
	// Nil when the listed name could not be resolved.
	ResolvedPlaceID *string
	// ResolvedVenueName is the canonical venue name resolved via Google Places.
	// Nil when unresolved.
	ResolvedVenueName *string
	// ResolvedAdminArea is the ISO 3166-2 admin area of the resolved venue.
	// Nil when unresolved or indeterminate.
	ResolvedAdminArea *string
	// ResolvedLatitude is the WGS 84 latitude of the resolved venue. Nil when
	// unresolved.
	ResolvedLatitude *float64
	// ResolvedLongitude is the WGS 84 longitude of the resolved venue. Nil when
	// unresolved.
	ResolvedLongitude *float64
	// DiscoveredTime is the timestamp when the discovery pipeline staged this
	// concert. Used to order the review queue.
	DiscoveredTime time.Time
}

// StagedConcertDedupKey is the pre-resolution dedup key used during discovery
// to avoid re-staging concerts that are already pending. It matches on the
// raw discovery-time identity (local_date, listed_venue_name) because venue
// resolution has not yet happened at search time.
type StagedConcertDedupKey struct {
	// LocalDate is the calendar date of the concert.
	LocalDate time.Time
	// ListedVenueName is the raw venue name as scraped from the source.
	ListedVenueName string
}

// StagedConcertRepository defines the data access interface for staged concerts.
type StagedConcertRepository interface {
	// Upsert inserts a new staged concert or refreshes an existing pending row
	// on natural-key conflict. The natural key branches on whether
	// ResolvedPlaceID is set: when non-nil it uses the
	// (artist_id, local_date, resolved_place_id) index; when nil it falls back
	// to (artist_id, local_date, listed_venue_name). On conflict the mutable
	// payload (title, start/open times, admin_area, source_url, resolved_*) is
	// updated but the original discovered_at is kept so queue order is stable.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: If artist_id does not exist in the artists table.
	//  - Internal: unexpected failure.
	Upsert(ctx context.Context, sc *StagedConcert) error

	// ListPending returns all staged concerts ordered by discovered_at ascending
	// (oldest first = review-queue order).
	ListPending(ctx context.Context) ([]*StagedConcert, error)

	// GetByID returns the staged concert with the given ID.
	//
	// # Possible errors
	//
	//  - NotFound: If no staged concert with that ID exists.
	GetByID(ctx context.Context, id string) (*StagedConcert, error)

	// Delete removes the staged concert with the given ID. It is idempotent:
	// deleting a row that does not exist returns no error.
	Delete(ctx context.Context, id string) error

	// ListPendingDedupKeysByArtist returns the (local_date, listed_venue_name)
	// pairs for all pending rows belonging to the given artist. The keys use
	// the raw discovery-time identity so they can be compared against scraped
	// concerts before venue resolution occurs.
	ListPendingDedupKeysByArtist(ctx context.Context, artistID string) ([]StagedConcertDedupKey, error)
}
