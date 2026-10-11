package rpc_test

import (
	"context"
	"net/http"
	"testing"

	artistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/artist/v1/artistv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	artistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/artist/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestArtistHandler_Create(t *testing.T) {
	t.Parallel()

	t.Run("passes MBID to use case", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

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
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

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

}

func TestArtistHandler_Boundary(t *testing.T) {
	t.Parallel()

	newClient := func(t *testing.T, artistUC *mocks.MockArtistUseCase) artistv1connect.ArtistServiceClient {
		t.Helper()
		logger, err := logging.New()
		require.NoError(t, err)
		h := handler.NewArtistHandler(artistUC, logger)
		srv := newFanBoundaryServer(t, func(opts ...connect.HandlerOption) (string, http.Handler) {
			return artistv1connect.NewArtistServiceHandler(h, opts...)
		})
		return artistv1connect.NewArtistServiceClient(srv.Client(), srv.URL)
	}

	t.Run("guest searches", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Guest searches"
		artistUC := mocks.NewMockArtistUseCase(t)
		artistUC.EXPECT().Search(mock.Anything, "beat").
			Return([]*entity.Artist{{ID: "artist-1", Name: "The Beatles"}}, nil).Once()

		resp, err := newClient(t, artistUC).Search(context.Background(), connect.NewRequest(&artistv1.SearchRequest{Query: "beat"}))

		require.NoError(t, err)
		require.Len(t, resp.Msg.GetArtists(), 1)
		assert.Equal(t, "artist-1", resp.Msg.GetArtists()[0].GetId().GetValue())
	})

	t.Run("guest creates an artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Guest creates an artist"
		// The mock fails the test if any usecase method runs.
		_, err := newClient(t, mocks.NewMockArtistUseCase(t)).Create(context.Background(), connect.NewRequest(&artistv1.CreateRequest{
			Name: &entityv1.ArtistName{Value: "The Beatles"},
			Mbid: &entityv1.Mbid{Value: "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d"},
		}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("empty search query", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Empty search query"
		_, err := newClient(t, mocks.NewMockArtistUseCase(t)).Search(context.Background(), connect.NewRequest(&artistv1.SearchRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("limit too large", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Limit too large"
		_, err := newClient(t, mocks.NewMockArtistUseCase(t)).ListTop(context.Background(), connect.NewRequest(&artistv1.ListTopRequest{Limit: 101}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("malformed MBID", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Malformed MBID"
		_, err := newClient(t, mocks.NewMockArtistUseCase(t)).Create(context.Background(), withFanToken(connect.NewRequest(&artistv1.CreateRequest{
			Name: &entityv1.ArtistName{Value: "The Beatles"},
			Mbid: &entityv1.Mbid{Value: "not-a-uuid"},
		})))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
