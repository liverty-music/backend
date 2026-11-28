package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type catalogArtistTestDeps struct {
	repo          *mocks.MockArtistRepository
	namesakes     *mocks.MockNamesakeCatalog
	officialSites *mocks.MockOfficialSiteCatalog
	idManager     *mocks.MockArtistIdentityManager
	uc            usecase.CatalogArtistUseCase
}

func newCatalogArtistTestDeps(t *testing.T) *catalogArtistTestDeps {
	t.Helper()
	d := &catalogArtistTestDeps{
		repo:          mocks.NewMockArtistRepository(t),
		namesakes:     mocks.NewMockNamesakeCatalog(t),
		officialSites: mocks.NewMockOfficialSiteCatalog(t),
		idManager:     mocks.NewMockArtistIdentityManager(t),
	}
	d.uc = usecase.NewCatalogArtistUseCase(d.repo, d.namesakes, d.officialSites, d.idManager)
	return d
}

func TestCatalogArtistUseCase_ListByArtist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	gb, jp := "GB", "JP"
	const jpSite = "https://penthouse.example/"

	// @spec components/usecase/catalog-artist/list-by-artist "Two namesakes"
	t.Run("Two namesakes", func(t *testing.T) {
		t.Parallel()
		d := newCatalogArtistTestDeps(t)
		uk := &entity.CatalogArtist{MBID: ukBand, Name: "Penthouse", Country: &gb, ActivityEnded: true}
		japanese := &entity.CatalogArtist{MBID: jpBand, Name: "Penthouse", Country: &jp}
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.namesakes.EXPECT().ListNamesakes(anyCtx, "Penthouse").Return([]*entity.CatalogArtist{uk, japanese}, nil).Once()
		d.officialSites.EXPECT().ListOfficialSiteRelations(anyCtx, ukBand).Return(homepageRelations("Penthouse", ""), nil).Once()
		d.officialSites.EXPECT().ListOfficialSiteRelations(anyCtx, jpBand).Return(homepageRelations("Penthouse", jpSite), nil).Once()

		got, err := d.uc.ListByArtist(ctx, "artist-1")

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, ukBand, got[0].MBID)
		assert.Nil(t, got[0].OfficialSiteURL, "the UK band has no official site")
		assert.Equal(t, jpBand, got[1].MBID)
		require.NotNil(t, got[1].OfficialSiteURL)
		assert.Equal(t, jpSite, *got[1].OfficialSiteURL)
	})

	// @spec components/usecase/catalog-artist/list-by-artist "Current MBID not found by name"
	t.Run("Current MBID not found by name", func(t *testing.T) {
		t.Parallel()
		d := newCatalogArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.namesakes.EXPECT().ListNamesakes(anyCtx, "Penthouse").Return([]*entity.CatalogArtist{
			{MBID: jpBand, Name: "Penthouse"}, {MBID: plBand, Name: "Penthouse"},
		}, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse (UK)", MBID: ukBand, ActivityEnded: true}, nil).Once()
		d.officialSites.EXPECT().ListOfficialSiteRelations(anyCtx, ukBand).Return(homepageRelations("Penthouse", ""), nil).Once()
		d.officialSites.EXPECT().ListOfficialSiteRelations(anyCtx, jpBand).Return(homepageRelations("Penthouse", jpSite), nil).Once()
		d.officialSites.EXPECT().ListOfficialSiteRelations(anyCtx, plBand).Return(homepageRelations("Penthouse", ""), nil).Once()

		got, err := d.uc.ListByArtist(ctx, "artist-1")

		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, []string{ukBand, jpBand, plBand}, []string{got[0].MBID, got[1].MBID, got[2].MBID})
		assert.Equal(t, "Penthouse (UK)", got[0].Name, "the catalog's name for the current MBID")
		assert.True(t, got[0].ActivityEnded, "the catalog's activity status for the current MBID")
		assert.Nil(t, got[0].Country)
		assert.Nil(t, got[0].Begin)
		assert.Nil(t, got[0].End)
		assert.Nil(t, got[0].Disambiguation)
	})

	// @spec components/usecase/catalog-artist/list-by-artist "Current MBID unknown to the catalog"
	t.Run("Current MBID unknown to the catalog", func(t *testing.T) {
		t.Parallel()
		d := newCatalogArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.namesakes.EXPECT().ListNamesakes(anyCtx, "Penthouse").Return([]*entity.CatalogArtist{{MBID: jpBand, Name: "Penthouse"}}, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(nil, apperr.New(codes.NotFound, "no such artist")).Once()
		d.officialSites.EXPECT().ListOfficialSiteRelations(anyCtx, jpBand).Return(homepageRelations("Penthouse", ""), nil).Once()

		got, err := d.uc.ListByArtist(ctx, "artist-1")

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, ukBand, got[0].MBID)
		assert.Equal(t, "Penthouse", got[0].Name, "the artist's registered name")
		assert.Nil(t, got[0].OfficialSiteURL)
		assert.Equal(t, jpBand, got[1].MBID)
	})

	// @spec components/usecase/catalog-artist/list-by-artist "Unknown artist"
	t.Run("Unknown artist", func(t *testing.T) {
		t.Parallel()
		d := newCatalogArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(nil, apperr.New(codes.NotFound, "artist not found")).Once()

		_, err := d.uc.ListByArtist(ctx, "artist-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/usecase/catalog-artist/list-by-artist "Catalog unavailable"
	t.Run("Catalog unavailable", func(t *testing.T) {
		t.Parallel()
		d := newCatalogArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.namesakes.EXPECT().ListNamesakes(anyCtx, "Penthouse").Return(nil, apperr.New(codes.Unavailable, "catalog down")).Once()

		_, err := d.uc.ListByArtist(ctx, "artist-1")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}
