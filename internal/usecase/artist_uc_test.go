package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
)

func TestArtistUseCase_CreateArtist(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		uc := usecase.NewArtistUseCase(repo, searcher, idManager, cache.NewMemoryCache(1*time.Hour), logger)

		artist := &entity.Artist{
			ID:   "artist-1",
			Name: "The Beatles",
			MBID: "5b11f448-2d57-455b-8292-629df8357062",
		}

		idManager.EXPECT().GetArtist(ctx, artist.MBID).Return(&entity.Artist{
			MBID: artist.MBID,
			Name: artist.Name,
		}, nil).Once()
		repo.EXPECT().Create(ctx, artist).Return(nil).Once()

		result, err := uc.Create(ctx, artist)

		assert.NoError(t, err)
		assert.Equal(t, artist, result)
	})

	t.Run("error - empty name", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		uc := usecase.NewArtistUseCase(repo, searcher, idManager, cache.NewMemoryCache(1*time.Hour), logger)

		artist := &entity.Artist{
			ID:   "artist-1",
			Name: "",
			MBID: "",
		}

		result, err := uc.Create(ctx, artist)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "artist name or MBID is required")
	})
}

func TestArtistUseCase_ListArtists(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		searcher := mocks.NewMockArtistSearcher(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		uc := usecase.NewArtistUseCase(repo, searcher, idManager, cache.NewMemoryCache(1*time.Hour), logger)

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
