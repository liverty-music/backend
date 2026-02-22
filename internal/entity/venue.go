package entity

import (
	"context"
)

// VenueEnrichmentStatus represents the normalization pipeline state for a venue.
type VenueEnrichmentStatus string

const (
	// EnrichmentStatusPending is the default state for newly created venues.
	EnrichmentStatusPending VenueEnrichmentStatus = "pending"
	// EnrichmentStatusEnriched indicates the venue has been successfully resolved to a canonical external ID.
	EnrichmentStatusEnriched VenueEnrichmentStatus = "enriched"
	// EnrichmentStatusFailed indicates the enrichment pipeline could not resolve the venue.
	EnrichmentStatusFailed VenueEnrichmentStatus = "failed"
)

// Venue represents a physical location where events are hosted.
//
// Corresponds to liverty_music.entity.v1.Venue.
type Venue struct {
	// ID is the unique identifier for the venue (UUID).
	ID string
	// Name is the name of the venue.
	Name string
	// AdminArea is the administrative area (prefecture, state, province) where the venue is located.
	// It is nil when the area could not be determined with confidence.
	AdminArea *string
	// MBID is the MusicBrainz Place ID for the canonical venue record.
	// Nil until the venue has been successfully enriched via MusicBrainz.
	MBID *string
	// GooglePlaceID is the Google Maps Place ID for the canonical venue record.
	// Nil until the venue has been successfully enriched via Google Maps.
	GooglePlaceID *string
	// EnrichmentStatus is the current state of the venue normalization pipeline.
	EnrichmentStatus VenueEnrichmentStatus
	// RawName is the original scraper-provided name before canonical renaming.
	// Preserved so that the original name can still be used as a lookup key after enrichment.
	RawName string
}

// NewVenue represents data for creating a new venue.
type NewVenue struct {
	// Name is the name of the venue to create.
	Name string
}

// VenuePlace represents a resolved canonical venue from an external place search service.
type VenuePlace struct {
	// ExternalID is the provider-specific identifier (MBID or Google Place ID).
	ExternalID string
	// Name is the canonical name returned by the external service.
	Name string
}

// VenuePlaceSearcher defines the interface for external place search services used in venue enrichment.
type VenuePlaceSearcher interface {
	// SearchPlace looks up a venue by name and optional administrative area.
	//
	// # Possible errors
	//
	//  - NotFound: If no matching place is found.
	//  - Unavailable: If the external service is unreachable.
	SearchPlace(ctx context.Context, name, adminArea string) (*VenuePlace, error)
}

// VenueEnrichmentRepository defines the data access interface for venue enrichment operations.
type VenueEnrichmentRepository interface {
	// ListPending returns all venues with enrichment_status = 'pending'.
	ListPending(ctx context.Context) ([]*Venue, error)

	// UpdateEnriched updates a venue to the enriched state, setting the canonical name,
	// external ID (MBID or GooglePlaceID), and preserving the raw name.
	UpdateEnriched(ctx context.Context, venue *Venue) error

	// MarkFailed sets enrichment_status = 'failed' for the given venue ID.
	MarkFailed(ctx context.Context, id string) error

	// MergeVenues merges a duplicate venue into a canonical venue within a single atomic
	// transaction. Events from the duplicate that share (artist_id, local_event_date, start_at)
	// with the canonical venue are deleted; remaining events are re-pointed to the canonical
	// venue. The canonical venue fields are updated via COALESCE for admin_area, mbid, and
	// google_place_id. The duplicate venue record is then deleted.
	MergeVenues(ctx context.Context, canonicalID, duplicateID string) error
}

// VenueRepository defines the data access interface for Venues.
type VenueRepository interface {
	// Create creates a new venue.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the venue name is invalid.
	Create(ctx context.Context, venue *Venue) error

	// Get retrieves a venue by ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the venue does not exist.
	Get(ctx context.Context, id string) (*Venue, error)

	// GetByName retrieves a venue by Name.
	//
	// # Possible errors
	//
	//  - NotFound: If the venue does not exist.
	GetByName(ctx context.Context, name string) (*Venue, error)
}
