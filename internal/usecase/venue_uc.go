package usecase

import (
	"context"
	"fmt"
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
}

// venueUseCase implements [VenueUseCase].
type venueUseCase struct {
	mapCatalog entity.VenueMapCatalog
}

var _ VenueUseCase = (*venueUseCase)(nil)

// NewVenueUseCase creates a VenueUseCase over the map catalog.
func NewVenueUseCase(mapCatalog entity.VenueMapCatalog) VenueUseCase {
	return &venueUseCase{mapCatalog: mapCatalog}
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
