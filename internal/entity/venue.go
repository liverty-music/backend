package entity

import (
	"context"
	"slices"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Venue represents a physical location where events are hosted.
//
// Corresponds to liverty_music.entity.v1.Venue.
type Venue struct {
	// ID is the unique identifier for the venue (UUID).
	ID string
	// Name is the name its source listed or the name a member saved; never a
	// name read from the map catalog that no member saved.
	Name string
	// AdminArea is the code of the venue's first-level Area (JP-13, HK). Nil
	// only for a venue stored before its area was known.
	AdminArea *string
	// TimeZone is the IANA zone of the venue: its Area's zone when the Area
	// has one, otherwise one of its country's. Nil only for a venue stored
	// before its zone was known.
	TimeZone *string
	// GooglePlaceID is the Google Maps Place ID for the canonical venue record.
	GooglePlaceID *string
	// Coordinates is the WGS 84 geographic position of the venue, read from
	// the map catalog at CoordinatesFetchTime.
	Coordinates *Coordinates
	// CoordinatesFetchTime is when the coordinates were read from the map
	// catalog; set exactly when Coordinates is.
	CoordinatesFetchTime *time.Time
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
	// SearchPlace looks up a venue by name and optional administrative area
	// and returns the places the service found, most relevant first, or an
	// empty slice when it found none. [SelectVenuePlace] picks the venue's
	// place among them.
	//
	// # Possible errors
	//
	//   - Unavailable: If the external service is unreachable.
	SearchPlace(ctx context.Context, name, adminArea string) ([]*VenuePlace, error)
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
	// Name is the place's name, in Japanese when the catalog has one.
	Name string
	// CountryCode is the ISO 3166-1 alpha-2 code of the place's country.
	CountryCode string
	// Coordinates is the WGS 84 position of the place. Nil when the catalog
	// gives none.
	Coordinates *Coordinates
	// CoordinatesFetchTime is when the place was read.
	CoordinatesFetchTime time.Time
	// SuggestedAdminArea is the code of the first-level Area the region of
	// the place's address stands for within its country, or the country
	// itself when it is its own first-level Area (HK). Nil when neither
	// applies.
	SuggestedAdminArea *string
	// TimeZone is the place's IANA time zone as the catalog gives it; nil
	// when it gives none.
	TimeZone *string
}

// PlaceCoordinates are the coordinates of a map place and when they were
// read; Coordinates is nil when the catalog gives the place no location.
type PlaceCoordinates struct {
	Coordinates *Coordinates
	FetchTime   time.Time
}

// VenueMapCatalog is the external map catalog a member picks a
// venue from. It is separate from VenuePlaceSearcher, which concert discovery
// uses to resolve a listed venue name and which MusicBrainz also implements.
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

	// GetPlaceCoordinates reads only the coordinates of a place, with the
	// time it read them. Coordinates is nil when the catalog gives the place
	// no location.
	//
	// # Possible errors
	//
	//  - NotFound: If the catalog has no place with that id.
	//  - Unavailable: If the catalog cannot be reached.
	GetPlaceCoordinates(ctx context.Context, placeID string) (*PlaceCoordinates, error)

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
	// so a lost race never surfaces a unique violation to the caller. Before
	// that it checks the admin area and time zone ([Venue.CheckArea]) and that
	// coordinates come with their fetch time.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the venue name is invalid, the admin area is
	//    no first-level Area, the time zone is missing or not one the Area
	//    allows, or coordinates come without their fetch time.
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

	// GetByListedName retrieves a venue by its exact listed venue name and
	// admin area, both required. This is used for a DB-first lookup to avoid
	// redundant Google Places API calls when the same venue has been resolved
	// in a previous discovery batch.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the name or the admin area is empty.
	//  - NotFound: If no venue with that listed name and admin area combination exists.
	GetByListedName(ctx context.Context, listedVenueName, adminArea string) (*Venue, error)

	// SetCoordinates replaces a venue's coordinates and their fetch time with
	// coords, or removes both when coords is nil. Nothing else on the venue
	// changes.
	//
	// # Possible errors
	//
	//  - InvalidArgument: coords has no coordinates.
	//  - NotFound: No venue has the id.
	SetCoordinates(ctx context.Context, venueID string, coords *PlaceCoordinates) error

	// ListCoordinatesDue returns at most limit venues with a place id whose
	// coordinates are missing or were fetched more than
	// [CoordinatesRefreshAge] before at: those without coordinates first,
	// then by fetch time, oldest first.
	//
	// # Possible errors
	//
	//  - InvalidArgument: limit is below 1.
	ListCoordinatesDue(ctx context.Context, at time.Time, limit int) ([]*Venue, error)

	// ClearExpiredCoordinates removes the coordinates and fetch time of every
	// venue whose coordinates were fetched more than [CoordinatesMaxAge]
	// before at, and returns how many venues it changed.
	ClearExpiredCoordinates(ctx context.Context, at time.Time) (int64, error)
}

// CoordinatesRefreshAge is the age after which a venue's coordinates are read
// again from the map catalog, ahead of [CoordinatesMaxAge].
const CoordinatesRefreshAge = 20 * 24 * time.Hour

// CoordinatesMaxAge is how long coordinates read from the map catalog may
// be kept.
const CoordinatesMaxAge = 30 * 24 * time.Hour

// CheckArea checks the venue's admin area and time zone against area, what
// its admin area resolved to, and zones, the time zones of the Area's
// country: the admin area is present and names that first-level Area, and the
// time zone is the Area's own when it has one, otherwise one of the
// country's.
//
// # Possible errors
//
//   - InvalidArgument: a rule does not hold.
func (v *Venue) CheckArea(area *Area, zones []string) error {
	if v.AdminArea == nil || area == nil || area.Code != *v.AdminArea || !area.IsFirstLevel() {
		return apperr.New(codes.InvalidArgument, "a venue's admin area must be a first-level Area")
	}
	if v.TimeZone == nil {
		return apperr.New(codes.InvalidArgument, "a venue must have a time zone")
	}
	if area.TimeZone != nil && *area.TimeZone != *v.TimeZone {
		return apperr.New(codes.InvalidArgument, "a venue's time zone must be its Area's own")
	}
	if !slices.Contains(zones, *v.TimeZone) {
		return apperr.New(codes.InvalidArgument, "a venue's time zone must be one of its country's")
	}
	return nil
}

// CheckCoordinates checks that the coordinates come with their fetch time.
//
// # Possible errors
//
//   - InvalidArgument: coordinates without a fetch time, or a fetch time
//     without coordinates.
func (v *Venue) CheckCoordinates() error {
	if (v.Coordinates == nil) != (v.CoordinatesFetchTime == nil) {
		return apperr.New(codes.InvalidArgument, "a venue's coordinates and their fetch time go together")
	}
	return nil
}

// CoordinatesExpired reports whether the coordinates were fetched more than
// [CoordinatesMaxAge] before now.
func (v *Venue) CoordinatesExpired(now time.Time) bool {
	return v.CoordinatesFetchTime != nil && now.Sub(*v.CoordinatesFetchTime) > CoordinatesMaxAge
}

// Location returns the venue's time zone, loaded from the binary's embedded
// zone data; nil when the venue has none or the name is unknown.
func (v *Venue) Location() *time.Location {
	if v.TimeZone == nil {
		return nil
	}
	loc, err := time.LoadLocation(*v.TimeZone)
	if err != nil {
		return nil
	}
	return loc
}

// VenueTimeZone returns the time zone of every Venue, which is Asia/Tokyo: a
// Venue carries no time zone of its own, so every Venue is taken to be in
// Japan. An Event's day at an overseas Venue is therefore off by that Venue's
// time difference from Japan. Giving a Venue its own time zone is tracked in
// https://github.com/liverty-music/specification/issues/1379; that changes
// what this function returns, not the rule that an Event's day is the day at
// its Venue.
func VenueTimeZone() *time.Location {
	return japanTime
}

// CurrentDateAtVenue returns the calendar day that t falls on at a Venue, in
// [VenueTimeZone], at midnight UTC like an Event's local date. Every
// comparison of an Event's local date with the current date, today or a date
// given as an instant, uses it, never the calendar day of the clock of the
// server or the database.
func CurrentDateAtVenue(t time.Time) time.Time {
	y, m, d := t.In(VenueTimeZone()).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
