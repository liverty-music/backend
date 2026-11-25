package usecase

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
)

// AreaUseCase reads the Area reference data for whoever confirms a venue's
// area or time zone: a member placing an event in the organizer console, or
// an admin approving a discovered concert. It stores nothing.
type AreaUseCase interface {
	// Resolve returns the first-level Area a text stands for, optionally
	// within one country.
	//
	// # Possible errors
	//
	// Those of [entity.AreaRepository.Resolve], unchanged: NotFound when the
	// text stands for no first-level Area or for more than one,
	// InvalidArgument for a malformed country code.
	Resolve(ctx context.Context, text string, country *string) (*entity.Area, error)

	// ListByCountry returns a country's first-level Areas, regions and time
	// zones.
	//
	// # Possible errors
	//
	// Those of [entity.AreaRepository.ListByCountry], unchanged.
	ListByCountry(ctx context.Context, country string) (*entity.CountryAreas, error)

	// ListCountries returns the countries and territories that have
	// first-level Areas.
	//
	// # Possible errors
	//
	// Those of [entity.AreaRepository.ListCountries], unchanged.
	ListCountries(ctx context.Context) ([]*entity.Country, error)
}

// areaUseCase implements [AreaUseCase].
type areaUseCase struct {
	areas entity.AreaRepository
}

var _ AreaUseCase = (*areaUseCase)(nil)

// NewAreaUseCase creates an AreaUseCase over the Area reference data.
func NewAreaUseCase(areas entity.AreaRepository) AreaUseCase {
	return &areaUseCase{areas: areas}
}

// Resolve implements [AreaUseCase].
func (uc *areaUseCase) Resolve(ctx context.Context, text string, country *string) (*entity.Area, error) {
	return uc.areas.Resolve(ctx, text, country)
}

// ListByCountry implements [AreaUseCase].
func (uc *areaUseCase) ListByCountry(ctx context.Context, country string) (*entity.CountryAreas, error) {
	return uc.areas.ListByCountry(ctx, country)
}

// ListCountries implements [AreaUseCase].
func (uc *areaUseCase) ListCountries(ctx context.Context) ([]*entity.Country, error) {
	return uc.areas.ListCountries(ctx)
}
