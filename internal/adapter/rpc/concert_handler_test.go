package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpcv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/v1"
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
		artistUC := mocks.NewMockArtistUseCase(t)
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(artistUC, concertUC, logger)
		_ = h // Basic test to ensure handler can be created with mocked UseCases
	})
}

func TestConcertHandler_SearchNewConcerts(t *testing.T) {
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		artistUC := mocks.NewMockArtistUseCase(t)
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(artistUC, concertUC, logger)

		artistID := "artist-123"
		mockConcerts := []*entity.Concert{
			{ID: "concert-1", ArtistID: artistID, Title: "New Show"},
		}

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(mockConcerts, nil)

		req := connect.NewRequest(&rpcv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, 1, len(resp.Msg.Concerts))
		assert.Equal(t, "New Show", resp.Msg.Concerts[0].Title.Value)
	})

	t.Run("failure", func(t *testing.T) {
		artistUC := mocks.NewMockArtistUseCase(t)
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(artistUC, concertUC, logger)

		artistID := "artist-123"
		expectedErr := assert.AnError

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(nil, expectedErr)

		req := connect.NewRequest(&rpcv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, expectedErr, err)
	})

	t.Run("invalid_argument", func(t *testing.T) {
		artistUC := mocks.NewMockArtistUseCase(t)
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(artistUC, concertUC, logger)

		req := connect.NewRequest(&rpcv1.SearchNewConcertsRequest{
			ArtistId: nil,
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
