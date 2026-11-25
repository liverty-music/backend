package usecase

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

const (
	// searchPlacesMinChars and searchPlacesMaxChars bound a venue search text,
	// counted in characters after trimming spaces.
	searchPlacesMinChars = 2
	searchPlacesMaxChars = 100
)

// PlaceDraft is what a picked place no Venue holds would become, for the
// member to edit or confirm before saving the event.
type PlaceDraft struct {
	// Name is the place's name in the map catalog.
	Name string
	// CountryCode is the ISO 3166-1 alpha-2 code of the place's country.
	CountryCode string
	// SuggestedAdminArea is the first-level Area the place's address
	// suggests; nil when it suggests none.
	SuggestedAdminArea *string
	// TimeZone is the suggested Area's zone, else the catalog's zone for the
	// place when it is one of the country's zones; nil otherwise.
	TimeZone *string
}

// ResolvedPlace is the result of ResolvePlace: exactly one of Venue and Draft
// is set.
type ResolvedPlace struct {
	// Venue is the Venue holding the place id.
	Venue *entity.Venue
	// Draft is the place draft when no Venue holds the place id.
	Draft *PlaceDraft
}

// VenueUseCase looks up venues in the map catalog for an organizer who is
// authoring a concert.
type VenueUseCase interface {
	// Search looks up a venue name in the map catalog for the caller's
	// Organizer while it authors a concert, and returns the catalog's
	// candidates unchanged. It stores nothing: a Venue is matched or created
	// only when a concert is saved with the picked place.
	//
	// # Possible errors
	//
	//  - InvalidArgument: The text is shorter than 2 or longer than 100
	//    characters after trimming spaces; the catalog is not called.
	//  - Unavailable: The map catalog cannot be reached.
	Search(ctx context.Context, callerOrgID, text string) ([]*entity.MapPlaceCandidate, error)

	// ResolvePlace tells a member who picked a place what it will be: the
	// Venue holding the place id (the map catalog is then not read), or a
	// place draft from the map catalog's place and its country's Areas. It
	// stores nothing.
	//
	// # Possible errors
	//
	//  - InvalidArgument: The place id is empty; nothing is read.
	//  - NotFound: The map catalog has no place with the id.
	//  - Unavailable: The map catalog cannot be reached.
	ResolvePlace(ctx context.Context, callerOrgID, placeID string) (*ResolvedPlace, error)
}

// venueUseCase implements [VenueUseCase].
type venueUseCase struct {
	mapCatalog entity.VenueMapCatalog
	venues     entity.VenueRepository
	areas      entity.AreaRepository
}

var _ VenueUseCase = (*venueUseCase)(nil)

// NewVenueUseCase creates a VenueUseCase over the map catalog, the Venues and
// the Area reference data.
func NewVenueUseCase(mapCatalog entity.VenueMapCatalog, venues entity.VenueRepository, areas entity.AreaRepository) VenueUseCase {
	return &venueUseCase{mapCatalog: mapCatalog, venues: venues, areas: areas}
}

// ResolvePlace implements [VenueUseCase].
func (uc *venueUseCase) ResolvePlace(ctx context.Context, _ string, placeID string) (*ResolvedPlace, error) {
	if strings.TrimSpace(placeID) == "" {
		return nil, apperr.New(codes.InvalidArgument, "place id is required")
	}
	venue, err := uc.venues.GetByPlaceID(ctx, placeID)
	if err == nil {
		return &ResolvedPlace{Venue: venue}, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return nil, fmt.Errorf("get venue by place id: %w", err)
	}

	place, err := uc.mapCatalog.GetPlace(ctx, placeID)
	if err != nil {
		return nil, fmt.Errorf("get place from map catalog: %w", err)
	}
	draft := &PlaceDraft{Name: place.Name, CountryCode: place.CountryCode, SuggestedAdminArea: place.SuggestedAdminArea}
	if place.CountryCode == "" {
		return &ResolvedPlace{Draft: draft}, nil
	}
	country, err := uc.areas.ListByCountry(ctx, place.CountryCode)
	if err != nil {
		return nil, fmt.Errorf("list areas of %s: %w", place.CountryCode, err)
	}
	draft.TimeZone = draftTimeZone(country, place)
	return &ResolvedPlace{Draft: draft}, nil
}

// draftTimeZone is the suggested Area's zone when it has one, else the
// catalog's zone for the place when it is one of the country's zones.
func draftTimeZone(country *entity.CountryAreas, place *entity.MapPlace) *string {
	if place.SuggestedAdminArea != nil {
		for _, a := range country.Areas {
			if a.Code == *place.SuggestedAdminArea && a.TimeZone != nil {
				return a.TimeZone
			}
		}
	}
	if place.TimeZone != nil && slices.ContainsFunc(country.TimeZones, func(z *entity.AreaTimeZone) bool {
		return z.Name == *place.TimeZone
	}) {
		return place.TimeZone
	}
	return nil
}

// Search implements [VenueUseCase].
func (uc *venueUseCase) Search(ctx context.Context, _ string, text string) ([]*entity.MapPlaceCandidate, error) {
	trimmed := strings.TrimSpace(text)
	if n := utf8.RuneCountInString(trimmed); n < searchPlacesMinChars || n > searchPlacesMaxChars {
		return nil, apperr.New(codes.InvalidArgument,
			fmt.Sprintf("venue search text must be %d to %d characters", searchPlacesMinChars, searchPlacesMaxChars))
	}
	candidates, err := uc.mapCatalog.SearchPlaces(ctx, trimmed)
	if err != nil {
		return nil, fmt.Errorf("search places in map catalog: %w", err)
	}
	return candidates, nil
}
