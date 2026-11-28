package rpc_test

import (
	"context"
	"testing"

	artistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/artist/v1/artistv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	artistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/artist/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestArtistHandler_Boundary(t *testing.T) {
	t.Parallel()

	newClient := func(t *testing.T, artistUC *mocks.MockArtistUseCase) artistv1connect.ArtistServiceClient {
		t.Helper()
		logger, err := logging.New()
		require.NoError(t, err)
		h := handler.NewArtistHandler(artistUC, logger)
		srv := newFanBoundaryServer(t, func(s *connect.Server) {
			artistv1connect.RegisterArtistServiceHandler(s, h)
		})
		return artistv1connect.NewArtistServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	}

	t.Run("guest searches", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Guest searches"
		artistUC := mocks.NewMockArtistUseCase(t)
		artistUC.EXPECT().Search(mock.Anything, "beat").
			Return([]*entity.Artist{{ID: "artist-1", Name: "The Beatles"}}, nil).Once()

		resp, err := newClient(t, artistUC).Search(context.Background(), &artistv1.SearchRequest{Query: "beat"})

		require.NoError(t, err)
		require.Len(t, resp.GetArtists(), 1)
		assert.Equal(t, "artist-1", resp.GetArtists()[0].GetId().GetValue())
	})

	t.Run("guest lists artists", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Guest lists artists"
		// The mock fails the test if any usecase method runs.
		_, err := newClient(t, mocks.NewMockArtistUseCase(t)).List(context.Background(), &artistv1.ListRequest{})

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("empty search query", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Empty search query"
		_, err := newClient(t, mocks.NewMockArtistUseCase(t)).Search(context.Background(), &artistv1.SearchRequest{})

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("limit too large", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Limit too large"
		_, err := newClient(t, mocks.NewMockArtistUseCase(t)).ListTop(context.Background(), &artistv1.ListTopRequest{Limit: 101})

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("similar-artist limit too large", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/artist "Similar-artist limit too large"
		// The mock fails the test if the usecase runs.
		_, err := newClient(t, mocks.NewMockArtistUseCase(t)).ListSimilar(context.Background(), &artistv1.ListSimilarRequest{
			ArtistId: &entityv1.ArtistId{Value: "0190b6a8-6a5e-7c3b-9e1a-2f4d5c6b7a81"},
			Limit:    51,
		})

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
