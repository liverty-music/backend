package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
)

// CatalogArtistUseCase reads the music catalog's artists for the admin
// console. Nothing it reads is stored.
type CatalogArtistUseCase interface {
	// ListByArtist returns the music catalog's artists whose name equals the
	// artist's name, in the catalog's order, each with its official homepage,
	// so an admin can choose the artist's MBID. When the artist's own MBID is
	// not among them it is listed first, with the catalog's name and activity
	// status for it (its registered name when the catalog no longer knows it),
	// so the current MBID is always one of the choices.
	//
	// # Possible errors:
	//
	//   - NotFound: no artist has the id. A namesake the catalog does not know
	//     is listed without an official site instead.
	//   - Unavailable: the catalog is down or rate-limited.
	//   - Internal: unexpected failure.
	ListByArtist(ctx context.Context, artistID string) ([]*entity.CatalogArtist, error)
}

// catalogArtistUseCase implements CatalogArtistUseCase.
type catalogArtistUseCase struct {
	artistRepo    entity.ArtistRepository
	namesakes     entity.NamesakeCatalog
	officialSites entity.OfficialSiteCatalog
	idManager     entity.ArtistIdentityManager
}

var _ CatalogArtistUseCase = (*catalogArtistUseCase)(nil)

// NewCatalogArtistUseCase creates the catalog artist use case.
func NewCatalogArtistUseCase(
	artistRepo entity.ArtistRepository,
	namesakes entity.NamesakeCatalog,
	officialSites entity.OfficialSiteCatalog,
	idManager entity.ArtistIdentityManager,
) CatalogArtistUseCase {
	return &catalogArtistUseCase{
		artistRepo:    artistRepo,
		namesakes:     namesakes,
		officialSites: officialSites,
		idManager:     idManager,
	}
}

// ListByArtist implements [CatalogArtistUseCase].
func (uc *catalogArtistUseCase) ListByArtist(ctx context.Context, artistID string) ([]*entity.CatalogArtist, error) {
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, err
	}
	namesakes, err := uc.namesakes.ListNamesakes(ctx, artist.Name)
	if err != nil {
		return nil, fmt.Errorf("list namesakes of artist %s: %w", artistID, err)
	}

	result := make([]*entity.CatalogArtist, 0, len(namesakes)+1)
	if !containsMBID(namesakes, artist.MBID) {
		current, err := uc.currentMBID(ctx, artist)
		if err != nil {
			return nil, err
		}
		result = append(result, current)
	}
	for _, n := range namesakes {
		// A namesake the catalog no longer knows (merged or deleted after its
		// search index was built) has no official site.
		site, err := officialSiteOf(ctx, uc.officialSites, n.MBID)
		if err != nil {
			return nil, err
		}
		n.OfficialSiteURL = nonEmptyURL(site)
		result = append(result, n)
	}
	return result, nil
}

// currentMBID returns the artist's own MBID as a CatalogArtist, with the
// catalog's name and activity status for it and its official homepage, or
// with the registered name and no homepage when the catalog does not know it.
func (uc *catalogArtistUseCase) currentMBID(ctx context.Context, artist *entity.Artist) (*entity.CatalogArtist, error) {
	current := &entity.CatalogArtist{MBID: artist.MBID, Name: artist.Name}
	catalog, err := uc.idManager.GetArtist(ctx, artist.MBID)
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		return current, nil
	case err != nil:
		return nil, fmt.Errorf("get artist %s from the catalog: %w", artist.MBID, err)
	}
	if catalog.Name != "" {
		current.Name = catalog.Name
	}
	current.ActivityEnded = catalog.ActivityEnded

	site, err := officialSiteOf(ctx, uc.officialSites, artist.MBID)
	if err != nil {
		return nil, err
	}
	current.OfficialSiteURL = nonEmptyURL(site)
	return current, nil
}

// containsMBID reports whether one of the catalog artists has the MBID.
func containsMBID(artists []*entity.CatalogArtist, mbid string) bool {
	for _, a := range artists {
		if a.MBID == mbid {
			return true
		}
	}
	return false
}

// nonEmptyURL returns a pointer to url, or nil when it is empty.
func nonEmptyURL(url string) *string {
	if url == "" {
		return nil
	}
	return &url
}
