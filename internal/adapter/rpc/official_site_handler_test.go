package rpc_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	officialsitev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/official_site/v1/official_sitev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	officialsitev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/official_site/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const officialSiteTestArtistID = "019a0000-0000-7000-8000-0000000000a1"

func newOfficialSiteHandler(t *testing.T, artistUC *mocks.MockOfficialSiteUseCase) *handler.OfficialSiteHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewOfficialSiteHandler(artistUC, logger)
}

func newOfficialSiteBoundaryClient(t *testing.T, artistUC *mocks.MockOfficialSiteUseCase) officialsitev1connect.OfficialSiteServiceClient {
	t.Helper()
	h := newOfficialSiteHandler(t, artistUC)
	srv := newFanBoundaryServer(t, func(opts ...connect.HandlerOption) (string, http.Handler) {
		return officialsitev1connect.NewOfficialSiteServiceHandler(h, opts...)
	})
	return officialsitev1connect.NewOfficialSiteServiceClient(srv.Client(), srv.URL)
}

func TestOfficialSiteHandler_Create(t *testing.T) {
	t.Parallel()

	t.Run("create a site", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/official-site "Create a site"
		artistUC := mocks.NewMockOfficialSiteUseCase(t)
		artistUC.EXPECT().Create(mock.Anything, officialSiteTestArtistID, "https://example.com").Return(nil).Once()

		resp, err := newOfficialSiteBoundaryClient(t, artistUC).Create(context.Background(), withFanToken(connect.NewRequest(&officialsitev1.CreateRequest{
			ArtistId: &entityv1.ArtistId{Value: officialSiteTestArtistID},
			Url:      &entityv1.Url{Value: "https://example.com"},
		})))

		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("guest creates a site", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/official-site "Guest creates a site"
		// The mock fails the test if any usecase method runs.
		_, err := newOfficialSiteBoundaryClient(t, mocks.NewMockOfficialSiteUseCase(t)).Create(context.Background(), connect.NewRequest(&officialsitev1.CreateRequest{
			ArtistId: &entityv1.ArtistId{Value: officialSiteTestArtistID},
			Url:      &entityv1.Url{Value: "https://example.com"},
		}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("malformed URL", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/official-site "Malformed URL"
		_, err := newOfficialSiteBoundaryClient(t, mocks.NewMockOfficialSiteUseCase(t)).Create(context.Background(), withFanToken(connect.NewRequest(&officialsitev1.CreateRequest{
			ArtistId: &entityv1.ArtistId{Value: officialSiteTestArtistID},
			Url:      &entityv1.Url{Value: "https://example.com/" + strings.Repeat("a", 2048)},
		})))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("return the usecase failure unchanged", func(t *testing.T) {
		t.Parallel()
		artistUC := mocks.NewMockOfficialSiteUseCase(t)
		artistUC.EXPECT().Create(mock.Anything, "artist-1", "https://example.com").Return(apperr.ErrAlreadyExists).Once()

		resp, err := newOfficialSiteHandler(t, artistUC).Create(context.Background(), connect.NewRequest(&officialsitev1.CreateRequest{
			ArtistId: &entityv1.ArtistId{Value: "artist-1"},
			Url:      &entityv1.Url{Value: "https://example.com"},
		}))

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		assert.Nil(t, resp)
	})
}

func TestOfficialSiteHandler_Delete(t *testing.T) {
	t.Parallel()

	t.Run("delete a site", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/official-site "Delete a site"
		// The mock fails the test if any usecase method runs.
		_, err := newOfficialSiteBoundaryClient(t, mocks.NewMockOfficialSiteUseCase(t)).Delete(context.Background(), withFanToken(connect.NewRequest(&officialsitev1.DeleteRequest{
			OfficialSiteId: &entityv1.OfficialSiteId{Value: "019a0000-0000-7000-8000-0000000000b1"},
		})))

		assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
	})
}
