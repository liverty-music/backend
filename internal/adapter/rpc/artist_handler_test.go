package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	artistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/v1/artist/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestArtistHandler_List(t *testing.T) {
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		artistUC := mocks.NewMockArtistUseCase(t)
		h := rpc.NewArtistHandler(artistUC, logger)

		mockArtists := []*entity.Artist{
			{ID: "artist-1", Name: "Artist One"},
		}
		artistUC.EXPECT().List(mock.Anything).Return(mockArtists, nil)

		req := connect.NewRequest(&artistv1.ListRequest{})
		resp, err := h.List(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, 1, len(resp.Msg.Artists))
		assert.Equal(t, "Artist One", resp.Msg.Artists[0].Name.Value)
	})
}

func TestArtistHandler_Create(t *testing.T) {
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		artistUC := mocks.NewMockArtistUseCase(t)
		h := rpc.NewArtistHandler(artistUC, logger)

		artistName := "New Artist"
		mockArtist := &entity.Artist{ID: "artist-1", Name: artistName}

		artistUC.EXPECT().Create(mock.Anything, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.Name == artistName
		})).Return(mockArtist, nil)

		req := connect.NewRequest(&artistv1.CreateRequest{
			Name: &entityv1.ArtistName{Value: artistName},
		})
		resp, err := h.Create(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, artistName, resp.Msg.Artist.Name.Value)
	})
}
