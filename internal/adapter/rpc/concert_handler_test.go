package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
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

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(nil)

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

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(expectedErr)

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
