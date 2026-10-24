package server_test

import (
	"context"
	"testing"

	adminartistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/artist/v1/artistv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/artist/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/liverty-music/backend/internal/entity"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestAdminServer_ArtistGet covers admin ArtistService.Get through the admin
// server's validation and role gate.
func TestAdminServer_ArtistGet(t *testing.T) {
	t.Parallel()

	const artistID = "019a0000-0000-7000-8000-0000000000a2"
	get := func(t *testing.T, artistUC *usecasemocks.MockArtistUseCase, token string, id *entityv1.ArtistId) (*adminartistv1.GetResponse, error) {
		t.Helper()
		ts := newTestAdminDeleteServer(t, usecasemocks.NewMockOrganizerUseCase(t), usecasemocks.NewMockUserUseCase(t), artistUC)
		client := adminartistv1connect.NewArtistServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))
		return client.Get(withBearer(context.Background(), token), &adminartistv1.GetRequest{ArtistId: id})
	}

	t.Run("admin reads an artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Admin reads an artist"
		artistUC := usecasemocks.NewMockArtistUseCase(t)
		artistUC.EXPECT().Get(mock.Anything, artistID).Return(&entity.Artist{ID: artistID, Name: "E2E Test Artist"}, nil).Once()

		resp, err := get(t, artistUC, deleteAdminToken, &entityv1.ArtistId{Value: artistID})

		require.NoError(t, err)
		assert.Equal(t, "E2E Test Artist", resp.GetArtist().GetName().GetValue())
	})

	t.Run("missing artist id", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Missing ArtistId"
		_, err := get(t, usecasemocks.NewMockArtistUseCase(t), deleteAdminToken, nil)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("unknown artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Unknown artist"
		artistUC := usecasemocks.NewMockArtistUseCase(t)
		artistUC.EXPECT().Get(mock.Anything, artistID).Return(nil, apperr.New(codes.NotFound, "artist not found")).Once()

		_, err := get(t, artistUC, deleteAdminToken, &entityv1.ArtistId{Value: artistID})

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("non-admin reads an artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Non-admin reads an artist"
		_, err := get(t, usecasemocks.NewMockArtistUseCase(t), deleteNonAdminToken, &entityv1.ArtistId{Value: artistID})

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}
