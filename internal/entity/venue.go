package entity

import (
	"context"
)

// Venue represents a physical location where events are hosted.
//
// Corresponds to liverty_music.entity.v1.Venue.
type Venue struct {
	// ID is the unique identifier for the venue (UUID).
	ID string
	// Name is the canonical name of the venue (from Google Places API).
	Name string
	// AdminArea is the administrative area (prefecture, state, province) where the venue is located.
	// It is nil when the area could not be determined with confidence.
	AdminArea *string
	// GooglePlaceID is the Google Maps Place ID for the canonical venue record.
	GooglePlaceID *string
	// Coordinates is the WGS 84 geographic position of the venue.
	Coordinates *Coordinates
	// ListedVenueName is the raw scraped venue name as returned by Gemini.
	// Used for DB-first lookup to avoid redundant Places API calls.
	// Nil for venues created before this field was introduced.
	ListedVenueName *string
}

// VenuePlace represents a resolved canonical venue from an external place search service.
type VenuePlace struct {
	// ProviderUserID is the Google Place ID.
	ProviderUserID string
	// Name is the canonical name returned by the external service.
	Name string
	// Coordinates is the WGS 84 geographic position returned by the external service.
	// Nil when the external service did not provide coordinate data.
	Coordinates *Coordinates
}

// VenuePlaceSearcher defines the interface for external place search services used in venue resolution.
type VenuePlaceSearcher interface {
	// SearchPlace looks up a venue by name and optional administrative area.
	//
	// # Possible errors
	//
	//  - NotFound: If no matching place is found.
	//  - Unavailable: If the external service is unreachable.
	SearchPlace(ctx context.Context, name, adminArea string) (*VenuePlace, error)
}

// MapPlaceCandidate is one place the map catalog offers for a venue name a
// member searched.
type MapPlaceCandidate struct {
	// PlaceID is the map catalog's place id (a Google Maps Place ID).
	PlaceID string
	// Name is the place's canonical name, in Japanese when the catalog has one.
	Name string
	// Address is the place's formatted address as the catalog gives it.
	Address string
}

// MapPlace is one place read from the map catalog by its place id.
type MapPlace struct {
	// PlaceID is the map catalog's place id (a Google Maps Place ID).
	PlaceID string
	// Name is the place's canonical name, in Japanese when the catalog has one.
	Name string
	// Coordinates is the WGS 84 position of the place. Nil when the catalog
	// gives none.
	Coordinates *Coordinates
	// AdminArea is the ISO 3166-2 code of the subdivision the place lies in
	// (e.g. "JP-13"). Nil when the catalog names no subdivision that maps to a
	// code.
	AdminArea *string
}

// VenueMapCatalog is the external map catalog a member picks a
// venue from. It is separate from VenuePlaceSearcher, which concert discovery
// uses for a single best match and which MusicBrainz also implements.
type VenueMapCatalog interface {
	// SearchPlaces looks up a venue name, which may be only the beginning of a
	// name as the member types it, and returns at most 5 candidate places,
	// best match first, places in Japan before places elsewhere. It returns an
	// empty slice when the catalog has no match.
	//
	// # Possible errors
	//
	//  - Unavailable: If the catalog cannot be reached.
	SearchPlaces(ctx context.Context, text string) ([]*MapPlaceCandidate, error)

	// GetPlace reads one place by its place id.
	//
	// # Possible errors
	//
	//  - NotFound: If the catalog has no place with that id.
	//  - Unavailable: If the catalog cannot be reached.
	GetPlace(ctx context.Context, placeID string) (*MapPlace, error)
}

// VenueRepository defines the data access interface for Venues.
type VenueRepository interface {
	// Create inserts a new venue and returns the id of the surviving row. It is an
	// idempotent get-or-create: the insert absorbs a conflict on either
	// place_id or (listed_venue_name, admin_area) and re-SELECTs the existing row,
	// so a lost race never surfaces a unique violation to the caller.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the venue name is invalid.
	Create(ctx context.Context, venue *Venue) (string, error)

	// BackfillPlaceID sets google_place_id on a venue that currently has none.
	// It is a no-op when the row already carries a non-NULL place_id or when a
	// concurrent backfill already set the value.
	//
	// # Possible errors
	//
	//  - Internal: If the update fails for a reason other than a benign race.
	BackfillPlaceID(ctx context.Context, venueID, placeID string) error

	// Get retrieves a venue by ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the venue does not exist.
	Get(ctx context.Context, id string) (*Venue, error)

	// GetByPlaceID retrieves a venue by Google Maps Place ID.
	//
	// # Possible errors
	//
	//  - NotFound: If no venue with that place ID exists.
	GetByPlaceID(ctx context.Context, placeID string) (*Venue, error)

	// GetByListedName retrieves a venue by its exact listed venue name and optional admin area.
	// This is used for a DB-first lookup to avoid redundant Google Places API calls
	// when the same venue has been resolved in a previous discovery batch.
	//
	// # Possible errors
	//
	//  - NotFound: If no venue with that listed name and admin area combination exists.
	GetByListedName(ctx context.Context, listedVenueName string, adminArea *string) (*Venue, error)
}
