package rpc_test

import (
	"context"
	"strings"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminofficialsitev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/official_site/v1"
	"connectrpc.com/connect/v2"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const officialSiteTestArtistID = "019a0000-0000-7000-8000-0000000000a1"

func newAdminOfficialSiteHandler(t *testing.T, officialSiteUC *mocks.MockOfficialSiteUseCase) *handler.AdminOfficialSiteHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewAdminOfficialSiteHandler(officialSiteUC, logger)
}

func officialSiteCreateRequest(artistID, url string) *adminofficialsitev1.CreateRequest {
	req := &adminofficialsitev1.CreateRequest{Url: &entityv1.Url{Value: url}}
	if artistID != "" {
		req.ArtistId = &entityv1.ArtistId{Value: artistID}
	}
	return req
}

func officialSiteUpdateRequest(artistID, url string) *adminofficialsitev1.UpdateRequest {
	req := &adminofficialsitev1.UpdateRequest{Url: &entityv1.Url{Value: url}}
	if artistID != "" {
		req.ArtistId = &entityv1.ArtistId{Value: artistID}
	}
	return req
}

func TestAdminOfficialSiteHandler_Create(t *testing.T) {
	t.Parallel()

	t.Run("admin creates a site", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/official-site "Admin creates a site"
		officialSiteUC := mocks.NewMockOfficialSiteUseCase(t)
		officialSiteUC.EXPECT().Create(mock.Anything, officialSiteTestArtistID, "https://example.com").Return(nil).Once()
		h := newAdminOfficialSiteHandler(t, officialSiteUC)

		resp, err := callThroughAdminPipeline(t, adminCtx(), officialSiteCreateRequest(officialSiteTestArtistID, "https://example.com"), h.Create)

		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("malformed URL", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/official-site "Malformed URL on Create"
		// The mock fails the test if any usecase method runs.
		h := newAdminOfficialSiteHandler(t, mocks.NewMockOfficialSiteUseCase(t))

		_, err := callThroughAdminPipeline(t, adminCtx(), officialSiteCreateRequest(officialSiteTestArtistID, "https://example.com/"+strings.Repeat("a", 2048)), h.Create)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("artist already has a site", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/official-site "Artist already has a site"
		officialSiteUC := mocks.NewMockOfficialSiteUseCase(t)
		officialSiteUC.EXPECT().Create(mock.Anything, officialSiteTestArtistID, "https://example.com").Return(apperr.ErrAlreadyExists).Once()
		h := newAdminOfficialSiteHandler(t, officialSiteUC)

		resp, err := callThroughAdminPipeline(t, adminCtx(), officialSiteCreateRequest(officialSiteTestArtistID, "https://example.com"), h.Create)

		assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
		assert.Nil(t, resp)
	})

	t.Run("unknown artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/official-site "Unknown artist"
		officialSiteUC := mocks.NewMockOfficialSiteUseCase(t)
		officialSiteUC.EXPECT().Create(mock.Anything, officialSiteTestArtistID, "https://example.com").Return(apperr.ErrFailedPrecondition).Once()
		h := newAdminOfficialSiteHandler(t, officialSiteUC)

		_, err := callThroughAdminPipeline(t, adminCtx(), officialSiteCreateRequest(officialSiteTestArtistID, "https://example.com"), h.Create)

		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})
}

func TestAdminOfficialSiteHandler_Update(t *testing.T) {
	t.Parallel()

	t.Run("admin corrects a site", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/official-site "Admin corrects a site"
		officialSiteUC := mocks.NewMockOfficialSiteUseCase(t)
		officialSiteUC.EXPECT().Update(mock.Anything, officialSiteTestArtistID, "https://www.example.com").Return(nil).Once()
		h := newAdminOfficialSiteHandler(t, officialSiteUC)

		resp, err := callThroughAdminPipeline(t, adminCtx(), officialSiteUpdateRequest(officialSiteTestArtistID, "https://www.example.com"), h.Update)

		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("artist without a site", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/official-site "Artist without a site"
		officialSiteUC := mocks.NewMockOfficialSiteUseCase(t)
		officialSiteUC.EXPECT().Update(mock.Anything, officialSiteTestArtistID, "https://www.example.com").Return(apperr.ErrNotFound).Once()
		h := newAdminOfficialSiteHandler(t, officialSiteUC)

		_, err := callThroughAdminPipeline(t, adminCtx(), officialSiteUpdateRequest(officialSiteTestArtistID, "https://www.example.com"), h.Update)

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("missing artist id", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/official-site "Missing artist id on Update"
		h := newAdminOfficialSiteHandler(t, mocks.NewMockOfficialSiteUseCase(t))

		_, err := callThroughAdminPipeline(t, adminCtx(), officialSiteUpdateRequest("", "https://www.example.com"), h.Update)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestAdminOfficialSiteHandler_Role(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context
		want connect.Code
	}{
		{"return PERMISSION_DENIED for a signed-in caller without the admin role", nonAdminCtx(), connect.CodePermissionDenied},
		{"return UNAUTHENTICATED for a caller who is not signed in", context.Background(), connect.CodeUnauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// No usecase has an expectation: reaching one fails the test.
			h := newAdminOfficialSiteHandler(t, mocks.NewMockOfficialSiteUseCase(t))

			// @spec components/adapter/admin/api/rpc/official-site "Signed-in fan creates a site"
			// @spec components/adapter/admin/api/rpc/official-site "Guest creates a site"
			_, err := callThroughAdminPipeline(t, tt.ctx, officialSiteCreateRequest(officialSiteTestArtistID, "https://example.com"), h.Create)
			assert.Equal(t, tt.want, connect.CodeOf(err), "Create")

			// @spec components/adapter/admin/api/rpc/official-site "Non-admin updates a site"
			_, err = callThroughAdminPipeline(t, tt.ctx, officialSiteUpdateRequest(officialSiteTestArtistID, "https://www.example.com"), h.Update)
			assert.Equal(t, tt.want, connect.CodeOf(err), "Update")
		})
	}
}
