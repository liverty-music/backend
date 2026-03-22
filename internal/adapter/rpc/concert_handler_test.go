package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestConcertHandler_List(t *testing.T) {
	t.Parallel()

	t.Run("returns concerts for a specific artist", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		artistID := "artist-123"
		localDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
		concertUC.EXPECT().ListByArtist(mock.Anything, artistID).Return([]*entity.Concert{
			{
				Event: entity.Event{
					ID:        "concert-1",
					VenueID:   "venue-1",
					Title:     "Summer Tour",
					LocalDate: localDate,
				},
				ArtistID: artistID,
			},
		}, nil).Once()

		req := connect.NewRequest(&concertv1.ListRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.List(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Msg.Concerts, 1)
		assert.Equal(t, "concert-1", resp.Msg.Concerts[0].Id.Value)
		assert.Equal(t, artistID, resp.Msg.Concerts[0].ArtistId.Value)
		assert.Equal(t, "venue-1", resp.Msg.Concerts[0].VenueId.Value)
		assert.Equal(t, "Summer Tour", resp.Msg.Concerts[0].Title.Value)
		assert.Equal(t, int32(2025), resp.Msg.Concerts[0].LocalDate.Value.Year)
		assert.Equal(t, int32(6), resp.Msg.Concerts[0].LocalDate.Value.Month)
		assert.Equal(t, int32(15), resp.Msg.Concerts[0].LocalDate.Value.Day)
	})

	t.Run("returns all concerts when artist_id is not specified", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		localDate := time.Date(2025, 7, 20, 0, 0, 0, 0, time.UTC)
		concertUC.EXPECT().ListByArtist(mock.Anything, "").Return([]*entity.Concert{
			{
				Event: entity.Event{
					ID:        "concert-2",
					VenueID:   "venue-2",
					Title:     "World Tour",
					LocalDate: localDate,
				},
				ArtistID: "artist-456",
			},
		}, nil).Once()

		req := connect.NewRequest(&concertv1.ListRequest{})

		resp, err := h.List(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Msg.Concerts, 1)
		assert.Equal(t, "concert-2", resp.Msg.Concerts[0].Id.Value)
	})

	t.Run("returns empty slice when no concerts exist", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		concertUC.EXPECT().ListByArtist(mock.Anything, "artist-999").Return([]*entity.Concert{}, nil).Once()

		req := connect.NewRequest(&concertv1.ListRequest{
			ArtistId: &entityv1.ArtistId{Value: "artist-999"},
		})

		resp, err := h.List(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Empty(t, resp.Msg.Concerts)
	})

	t.Run("propagates use case error", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		concertUC.EXPECT().ListByArtist(mock.Anything, "artist-123").Return(nil, assert.AnError).Once()

		req := connect.NewRequest(&concertv1.ListRequest{
			ArtistId: &entityv1.ArtistId{Value: "artist-123"},
		})

		resp, err := h.List(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestConcertHandler_SearchNewConcerts(t *testing.T) {
	t.Parallel()

	t.Run("success_returns_concerts", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		artistID := "artist-123"
		date := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		venueName := "Tokyo Dome"
		concerts := []*entity.Concert{
			{
				Event: entity.Event{
					ID: "c1", Title: "Summer Live",
					ListedVenueName: &venueName,
					LocalDate:       date,
				},
				ArtistID: artistID,
			},
		}

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(concerts, nil)

		req := connect.NewRequest(&concertv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Msg.Concerts, 1)
		assert.Equal(t, "Summer Live", resp.Msg.Concerts[0].Title.GetValue())
	})

	t.Run("success_no_concerts", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		artistID := "artist-123"

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(nil, nil)

		req := connect.NewRequest(&concertv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Empty(t, resp.Msg.Concerts)
	})

	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		artistID := "artist-123"

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(nil, assert.AnError)

		req := connect.NewRequest(&concertv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("invalid_argument", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		req := connect.NewRequest(&concertv1.SearchNewConcertsRequest{
			ArtistId: nil,
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestConcertHandler_ListByFollower(t *testing.T) {
	t.Parallel()

	t.Run("unauthenticated", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		// No auth context → GetUserID returns false → UNAUTHENTICATED
		req := connect.NewRequest(&concertv1.ListByFollowerRequest{})

		resp, err := h.ListByFollower(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}
