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
	"github.com/liverty-music/backend/internal/usecase"
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

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		artistID := "artist-123"

		concertUC.EXPECT().AsyncSearchNewConcerts(mock.Anything, artistID).Return(nil)

		req := connect.NewRequest(&concertv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		artistID := "artist-123"
		expectedErr := assert.AnError

		concertUC.EXPECT().AsyncSearchNewConcerts(mock.Anything, artistID).Return(expectedErr)

		req := connect.NewRequest(&concertv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, expectedErr, err)
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

func TestConcertHandler_ListSearchStatuses(t *testing.T) {
	t.Parallel()

	t.Run("success_multiple_statuses", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		concertUC.EXPECT().ListSearchStatuses(mock.Anything, []string{"a1", "a2", "a3"}).
			Return([]*usecase.SearchStatus{
				{ArtistID: "a1", Status: usecase.SearchStatusCompleted},
				{ArtistID: "a2", Status: usecase.SearchStatusPending},
				{ArtistID: "a3", Status: usecase.SearchStatusFailed},
			}, nil)

		req := connect.NewRequest(&concertv1.ListSearchStatusesRequest{
			ArtistIds: []*entityv1.ArtistId{
				{Value: "a1"},
				{Value: "a2"},
				{Value: "a3"},
			},
		})

		resp, err := h.ListSearchStatuses(context.Background(), req)

		assert.NoError(t, err)
		assert.Len(t, resp.Msg.Statuses, 3)
		assert.Equal(t, "a1", resp.Msg.Statuses[0].ArtistId.Value)
		assert.Equal(t, concertv1.SearchStatus_SEARCH_STATUS_COMPLETED, resp.Msg.Statuses[0].Status)
		assert.Equal(t, "a2", resp.Msg.Statuses[1].ArtistId.Value)
		assert.Equal(t, concertv1.SearchStatus_SEARCH_STATUS_PENDING, resp.Msg.Statuses[1].Status)
		assert.Equal(t, "a3", resp.Msg.Statuses[2].ArtistId.Value)
		assert.Equal(t, concertv1.SearchStatus_SEARCH_STATUS_FAILED, resp.Msg.Statuses[2].Status)
	})

	t.Run("empty_artist_ids", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		req := connect.NewRequest(&concertv1.ListSearchStatusesRequest{
			ArtistIds: nil,
		})

		resp, err := h.ListSearchStatuses(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("usecase_error", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		concertUC.EXPECT().ListSearchStatuses(mock.Anything, []string{"a1"}).
			Return(nil, assert.AnError)

		req := connect.NewRequest(&concertv1.ListSearchStatusesRequest{
			ArtistIds: []*entityv1.ArtistId{{Value: "a1"}},
		})

		resp, err := h.ListSearchStatuses(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("unspecified_status", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		concertUC.EXPECT().ListSearchStatuses(mock.Anything, []string{"unknown"}).
			Return([]*usecase.SearchStatus{
				{ArtistID: "unknown", Status: usecase.SearchStatusUnspecified},
			}, nil)

		req := connect.NewRequest(&concertv1.ListSearchStatusesRequest{
			ArtistIds: []*entityv1.ArtistId{{Value: "unknown"}},
		})

		resp, err := h.ListSearchStatuses(context.Background(), req)

		assert.NoError(t, err)
		assert.Len(t, resp.Msg.Statuses, 1)
		assert.Equal(t, concertv1.SearchStatus_SEARCH_STATUS_UNSPECIFIED, resp.Msg.Statuses[0].Status)
	})
}
