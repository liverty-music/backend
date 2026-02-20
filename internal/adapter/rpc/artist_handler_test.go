package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	artistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/artist/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestArtistHandler_Create(t *testing.T) {
	logger, _ := logging.New()

	t.Run("passes MBID to use case", func(t *testing.T) {
		artistUC := mocks.NewMockArtistUseCase(t)
		h := handler.NewArtistHandler(artistUC, logger)

		artistUC.EXPECT().Create(mock.Anything, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.Name == "The Beatles" && a.MBID == "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d"
		})).Return(&entity.Artist{
			ID:   "artist-1",
			Name: "The Beatles",
			MBID: "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d",
		}, nil).Once()

		req := connect.NewRequest(&artistv1.CreateRequest{
			Name: &entityv1.ArtistName{Value: "The Beatles"},
			Mbid: &entityv1.Mbid{Value: "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d"},
		})

		resp, err := h.Create(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d", resp.Msg.Artist.Mbid.Value)
	})

	t.Run("works without MBID", func(t *testing.T) {
		artistUC := mocks.NewMockArtistUseCase(t)
		h := handler.NewArtistHandler(artistUC, logger)

		artistUC.EXPECT().Create(mock.Anything, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.Name == "Unknown" && a.MBID == ""
		})).Return(&entity.Artist{
			ID:   "artist-2",
			Name: "Unknown",
		}, nil).Once()

		req := connect.NewRequest(&artistv1.CreateRequest{
			Name: &entityv1.ArtistName{Value: "Unknown"},
		})

		resp, err := h.Create(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("error - missing name", func(t *testing.T) {
		artistUC := mocks.NewMockArtistUseCase(t)
		h := handler.NewArtistHandler(artistUC, logger)

		req := connect.NewRequest(&artistv1.CreateRequest{})

		_, err := h.Create(context.Background(), req)

		assert.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
