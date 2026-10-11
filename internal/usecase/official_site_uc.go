package usecase

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
)

// OfficialSiteUseCase records an artist's official site.
type OfficialSiteUseCase interface {
	// Create mints a fresh OfficialSite for the artist from the given
	// URL and persists it. An artist has at most one official site.
	//
	// # Possible errors:
	//
	//   - AlreadyExists: the artist already has an official site.
	//   - FailedPrecondition: no artist exists with the given artistID.
	//   - Internal: database execution failure.
	Create(ctx context.Context, artistID, url string) error
}

// officialSiteUseCase implements [OfficialSiteUseCase].
type officialSiteUseCase struct {
	artistRepo entity.ArtistRepository
}

var _ OfficialSiteUseCase = (*officialSiteUseCase)(nil)

// NewOfficialSiteUseCase creates an OfficialSiteUseCase.
func NewOfficialSiteUseCase(artistRepo entity.ArtistRepository) OfficialSiteUseCase {
	return &officialSiteUseCase{artistRepo: artistRepo}
}

// Create implements [OfficialSiteUseCase].
func (uc *officialSiteUseCase) Create(ctx context.Context, artistID, url string) error {
	return uc.artistRepo.CreateOfficialSite(ctx, entity.NewOfficialSite(artistID, url))
}
