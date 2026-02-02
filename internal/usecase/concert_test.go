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

func TestConcertUseCase_ListConcertsByArtist(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		concertRepo := mocks.NewMockConcertRepository(t)
		searcher := mocks.NewMockConcertSearcher(t)
		uc := usecase.NewConcertUseCase(artistRepo, concertRepo, searcher, logger)

		concerts := []*entity.Concert{
			{ID: "c1", ArtistID: "a1", Title: "Concert 1"},
		}

		concertRepo.EXPECT().ListByArtist(ctx, "a1", false).Return(concerts, nil).Once()

		result, err := uc.ListByArtist(ctx, "a1")

		assert.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, concerts, result)
	})

	t.Run("error - empty artist ID", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		concertRepo := mocks.NewMockConcertRepository(t)
		searcher := mocks.NewMockConcertSearcher(t)
		uc := usecase.NewConcertUseCase(artistRepo, concertRepo, searcher, logger)

		result, err := uc.ListByArtist(ctx, "")

		assert.Error(t, err)
		assert.Nil(t, result)
	})
}
