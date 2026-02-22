package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// anyCtx matches any context.Context regardless of type (e.g. context.WithoutCancel).
var anyCtx = mock.MatchedBy(func(context.Context) bool { return true })

func newTestArtistUC(t *testing.T, repo *mocks.MockArtistRepository, searcher *mocks.MockArtistSearcher, idManager *mocks.MockArtistIdentityManager, siteResolver *mocks.MockOfficialSiteResolver, logger *logging.Logger) usecase.ArtistUseCase {
	t.Helper()
	return usecase.NewArtistUseCase(repo, searcher, idManager, siteResolver, cache.NewMemoryCache(1*time.Hour), logger)
}

func TestArtistUseCase_CreateArtist(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		artist := &entity.Artist{
			ID:   "artist-1",
			Name: "The Beatles",
			MBID: "5b11f448-2d57-455b-8292-629df8357062",
		}

		idManager.EXPECT().GetArtist(ctx, artist.MBID).Return(&entity.Artist{
			MBID: artist.MBID,
			Name: artist.Name,
		}, nil).Once()
		repo.EXPECT().Create(ctx, artist).Return([]*entity.Artist{artist}, nil).Once()

		result, err := uc.Create(ctx, artist)

		assert.NoError(t, err)
		assert.Equal(t, artist, result)
	})

	t.Run("error - empty name", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		artist := &entity.Artist{
			ID:   "artist-1",
			Name: "",
			MBID: "",
		}

		result, err := uc.Create(ctx, artist)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Nil(t, result)
	})
}

func TestArtistUseCase_ListArtists(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		artists := []*entity.Artist{
			{ID: "1", Name: "Artist 1"},
			{ID: "2", Name: "Artist 2"},
		}

		repo.EXPECT().List(ctx).Return(artists, nil).Once()

		result, err := uc.List(ctx)

		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, artists, result)
	})
}

func TestArtistUseCase_ListTop(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("returns persisted artists with valid IDs", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		fetched := []*entity.Artist{
			{Name: "Artist A", MBID: "mbid-a"},
			{Name: "Artist B", MBID: "mbid-b"},
		}
		persisted := []*entity.Artist{
			{ID: "id-a", Name: "Artist A", MBID: "mbid-a"},
			{ID: "id-b", Name: "Artist B", MBID: "mbid-b"},
		}

		searcher.EXPECT().ListTop(ctx, "JP").Return(fetched, nil).Once()
		repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist"), mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		result, err := uc.ListTop(ctx, "JP")

		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "id-a", result[0].ID)
		assert.Equal(t, "id-b", result[1].ID)
	})

	t.Run("error - searcher fails", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := usecase.NewArtistUseCase(repo, searcher, idManager, siteResolver, cache.NewMemoryCache(1*time.Hour), logger)

		searcher.EXPECT().ListTop(ctx, "JP").Return(nil, apperr.ErrInternal).Once()

		result, err := uc.ListTop(ctx, "JP")

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Nil(t, result)
	})

	t.Run("returns cached results on second call", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		persisted := []*entity.Artist{
			{ID: "id-a", Name: "Artist A", MBID: "mbid-a"},
		}

		searcher.EXPECT().ListTop(ctx, "US").Return([]*entity.Artist{{Name: "Artist A", MBID: "mbid-a"}}, nil).Once()
		repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		// First call — cache miss
		_, err := uc.ListTop(ctx, "US")
		assert.NoError(t, err)

		// Second call — cache hit (no additional mock calls expected)
		result, err := uc.ListTop(ctx, "US")
		assert.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestArtistUseCase_ListSimilar(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("error - seed artist not found", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := usecase.NewArtistUseCase(repo, searcher, idManager, siteResolver, cache.NewMemoryCache(1*time.Hour), logger)

		repo.EXPECT().Get(ctx, "missing-id").Return(nil, apperr.ErrNotFound).Once()

		result, err := uc.ListSimilar(ctx, "missing-id")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Nil(t, result)
	})

	t.Run("returns persisted artists with valid IDs", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		seedArtist := &entity.Artist{ID: "seed-id", Name: "Seed", MBID: "seed-mbid"}
		fetched := []*entity.Artist{
			{Name: "Similar A", MBID: "sim-a"},
			{Name: "Similar B", MBID: "sim-b"},
		}
		persisted := []*entity.Artist{
			{ID: "id-sim-a", Name: "Similar A", MBID: "sim-a"},
			{ID: "id-sim-b", Name: "Similar B", MBID: "sim-b"},
		}

		repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
		searcher.EXPECT().ListSimilar(ctx, seedArtist).Return(fetched, nil).Once()
		repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist"), mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		result, err := uc.ListSimilar(ctx, "seed-id")

		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "id-sim-a", result[0].ID)
		assert.Equal(t, "id-sim-b", result[1].ID)
	})
}

func TestArtistUseCase_Follow(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	artist := &entity.Artist{ID: "artist-1", Name: "saji", MBID: "f71a9739-5d53-404f-a813-0925894c673b"}

	t.Run("success - resolves and persists official site", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		repo.EXPECT().Follow(ctx, "user-1", artist.ID).Return(nil).Once()
		// Goroutine uses context.WithoutCancel — match any context
		repo.EXPECT().GetOfficialSite(anyCtx, artist.ID).Return(nil, apperr.ErrNotFound).Once()
		repo.EXPECT().Get(anyCtx, artist.ID).Return(artist, nil).Once()
		siteResolver.EXPECT().ResolveOfficialSiteURL(anyCtx, artist.MBID).Return("https://saji.tokyo/", nil).Once()
		repo.EXPECT().CreateOfficialSite(anyCtx, mock.MatchedBy(func(site *entity.OfficialSite) bool {
			return site.ArtistID == artist.ID && site.URL == "https://saji.tokyo/" && site.ID != ""
		})).Return(nil).Once()

		err := uc.Follow(ctx, "user-1", artist.ID)
		assert.NoError(t, err)

		// Give the goroutine time to complete
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("success - skips resolution when official site already exists", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		repo.EXPECT().Follow(ctx, "user-1", artist.ID).Return(nil).Once()
		repo.EXPECT().GetOfficialSite(anyCtx, artist.ID).Return(&entity.OfficialSite{
			ID: "site-1", ArtistID: artist.ID, URL: "https://saji.tokyo/",
		}, nil).Once()

		err := uc.Follow(ctx, "user-1", artist.ID)
		assert.NoError(t, err)

		time.Sleep(50 * time.Millisecond)
	})

	t.Run("success - resolver returns empty URL, no site persisted", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		repo.EXPECT().Follow(ctx, "user-1", artist.ID).Return(nil).Once()
		repo.EXPECT().GetOfficialSite(anyCtx, artist.ID).Return(nil, apperr.ErrNotFound).Once()
		repo.EXPECT().Get(anyCtx, artist.ID).Return(artist, nil).Once()
		siteResolver.EXPECT().ResolveOfficialSiteURL(anyCtx, artist.MBID).Return("", nil).Once()

		err := uc.Follow(ctx, "user-1", artist.ID)
		assert.NoError(t, err)

		time.Sleep(50 * time.Millisecond)
	})

	t.Run("success - resolver errors, follow still succeeds", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		repo.EXPECT().Follow(ctx, "user-1", artist.ID).Return(nil).Once()
		repo.EXPECT().GetOfficialSite(anyCtx, artist.ID).Return(nil, apperr.ErrNotFound).Once()
		repo.EXPECT().Get(anyCtx, artist.ID).Return(artist, nil).Once()
		siteResolver.EXPECT().ResolveOfficialSiteURL(anyCtx, artist.MBID).Return("", errors.New("network error")).Once()

		err := uc.Follow(ctx, "user-1", artist.ID)
		assert.NoError(t, err)

		time.Sleep(50 * time.Millisecond)
	})

	t.Run("success - CreateOfficialSite race (AlreadyExists), follow still succeeds", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		siteResolver := mocks.NewMockOfficialSiteResolver(t)
		uc := newTestArtistUC(t, repo, searcher, idManager, siteResolver, logger)

		repo.EXPECT().Follow(ctx, "user-1", artist.ID).Return(nil).Once()
		repo.EXPECT().GetOfficialSite(anyCtx, artist.ID).Return(nil, apperr.ErrNotFound).Once()
		repo.EXPECT().Get(anyCtx, artist.ID).Return(artist, nil).Once()
		siteResolver.EXPECT().ResolveOfficialSiteURL(anyCtx, artist.MBID).Return("https://saji.tokyo/", nil).Once()
		// Concurrent re-follow: another goroutine already inserted the record
		repo.EXPECT().CreateOfficialSite(anyCtx, mock.MatchedBy(func(site *entity.OfficialSite) bool {
			return site.ArtistID == artist.ID && site.URL == "https://saji.tokyo/" && site.ID != ""
		})).Return(apperr.ErrAlreadyExists).Once()

		err := uc.Follow(ctx, "user-1", artist.ID)
		assert.NoError(t, err)

		time.Sleep(50 * time.Millisecond)
	})
}
