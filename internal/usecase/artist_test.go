package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
)

func TestArtistUseCase_CreateArtist(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		uc := usecase.NewArtistUseCase(repo, logger)

		artist := &entity.Artist{
			ID:   "artist-1",
			Name: "The Beatles",
		}

		repo.EXPECT().Create(ctx, artist).Return(nil).Once()

		result, err := uc.Create(ctx, artist)

		assert.NoError(t, err)
		assert.Equal(t, artist, result)
	})

	t.Run("error - empty name", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		uc := usecase.NewArtistUseCase(repo, logger)

		artist := &entity.Artist{
			ID:   "artist-1",
			Name: "",
		}

		result, err := uc.Create(ctx, artist)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "name is required")
	})
}

func TestArtistUseCase_ListArtists(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		repo := mocks.NewMockArtistRepository(t)
		uc := usecase.NewArtistUseCase(repo, logger)

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
