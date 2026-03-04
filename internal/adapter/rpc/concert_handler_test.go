package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestConcertHandler_List(t *testing.T) {
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)
		_ = h // Basic test to ensure handler can be created with mocked UseCases
	})
}

func TestConcertHandler_SearchNewConcerts(t *testing.T) {
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
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
	logger, _ := logging.New()

	t.Run("unauthenticated", func(t *testing.T) {
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
	logger, _ := logging.New()

	t.Run("success_multiple_statuses", func(t *testing.T) {
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		concertUC.EXPECT().ListSearchStatuses(mock.Anything, []string{"a1", "a2", "a3"}).
			Return([]*entity.SearchStatus{
				{ArtistID: "a1", Status: entity.SearchStatusCompleted},
				{ArtistID: "a2", Status: entity.SearchStatusPending},
				{ArtistID: "a3", Status: entity.SearchStatusFailed},
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
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(concertUC, logger)

		concertUC.EXPECT().ListSearchStatuses(mock.Anything, []string{"unknown"}).
			Return([]*entity.SearchStatus{
				{ArtistID: "unknown", Status: entity.SearchStatusUnspecified},
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
