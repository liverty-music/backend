package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestConcertUseCase_ListConcertsByArtist(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		concertRepo := mocks.NewMockConcertRepository(t)
		venueRepo := mocks.NewMockVenueRepository(t)
		searcher := mocks.NewMockConcertSearcher(t)
		uc := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, searcher, logger)

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
		venueRepo := mocks.NewMockVenueRepository(t)
		searcher := mocks.NewMockConcertSearcher(t)
		uc := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, searcher, logger)

		result, err := uc.ListByArtist(ctx, "")

		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestConcertUseCase_SearchNewConcerts(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success - new venue created", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		concertRepo := mocks.NewMockConcertRepository(t)
		venueRepo := mocks.NewMockVenueRepository(t)
		searcher := mocks.NewMockConcertSearcher(t)
		uc := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, searcher, logger)

		artistID := "artist-1"
		artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
		site := &entity.OfficialSite{ArtistID: artistID, URL: "https://example.com"}

		scraped := []*entity.ScrapedConcert{
			{
				Title:          "New Concert",
				VenueName:      "New Venue",
				LocalEventDate: time.Now().Add(24 * time.Hour),
				SourceURL:      "https://example.com/concert",
			},
		}

		// Expectations
		artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
		artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(site, nil).Once()
		concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
		searcher.EXPECT().Search(ctx, artist, site, mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()

		// Venue logic: GetByName fails with NotFound, then Create
		venueRepo.EXPECT().GetByName(ctx, "New Venue").Return(nil, apperr.New(codes.NotFound, "not found")).Once()
		venueRepo.EXPECT().Create(ctx, mock.MatchedBy(func(v *entity.Venue) bool {
			return v.Name == "New Venue" && v.ID != ""
		})).Return(nil).Once()

		// Concert logic: Create
		concertRepo.EXPECT().Create(ctx, mock.MatchedBy(func(c *entity.Concert) bool {
			return c.ArtistID == artistID && c.Title == "New Concert" && c.ID != ""
		})).Return(nil).Once()

		// Execution
		result, err := uc.SearchNewConcerts(ctx, artistID)

		assert.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "New Concert", result[0].Title)
		assert.NotEmpty(t, result[0].ID)
		assert.NotEmpty(t, result[0].VenueID)
	})
}
