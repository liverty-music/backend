package rpc_test

import (
	"context"
	"fmt"
	"testing"

	entitypb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminnotificationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/notification/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	apperrcodes "github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// devConfig returns a BaseConfig representing a non-production environment
// so the test hook is enabled under test.
func devConfig() config.BaseConfig {
	return config.BaseConfig{Environment: "development"}
}

// prodConfig returns a BaseConfig representing production so the test hook
// is gated at the handler.
func prodConfig() config.BaseConfig {
	return config.BaseConfig{Environment: "production"}
}

const notifyTestArtistID = "019a0000-0000-7000-8000-0000000000a1"

func newAdminNotificationHandler(t *testing.T, uc *ucmocks.MockPushSubscriptionUseCase, cfg config.BaseConfig) *handler.AdminNotificationHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewAdminNotificationHandler(uc, cfg, logger)
}

func notifyRequest(concertCount int) *adminnotificationv1.NotifyNewConcertsRequest {
	ids := make([]*entitypb.EventId, 0, concertCount)
	for i := range concertCount {
		ids = append(ids, &entitypb.EventId{Value: fmt.Sprintf("019a0000-0000-7000-8000-%012d", i)})
	}
	return &adminnotificationv1.NotifyNewConcertsRequest{
		ArtistId:   &entitypb.ArtistId{Value: notifyTestArtistID},
		ConcertIds: ids,
	}
}

func TestAdminNotificationHandler_NotifyNewConcerts(t *testing.T) {
	t.Parallel()

	t.Run("non-production call", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/notification "Non-production call"
		uc := ucmocks.NewMockPushSubscriptionUseCase(t)
		req := notifyRequest(2)
		uc.EXPECT().NotifyNewConcerts(mock.Anything, usecase.ConcertCreatedData{
			ArtistID:   notifyTestArtistID,
			ConcertIDs: []string{req.GetConcertIds()[0].GetValue(), req.GetConcertIds()[1].GetValue()},
		}).Return(nil).Once()
		h := newAdminNotificationHandler(t, uc, devConfig())

		resp, err := callThroughAdminPipeline(t, adminCtx(), req, h.NotifyNewConcerts)

		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("production", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/notification "Production"
		h := newAdminNotificationHandler(t, ucmocks.NewMockPushSubscriptionUseCase(t), prodConfig())

		_, err := callThroughAdminPipeline(t, adminCtx(), notifyRequest(2), h.NotifyNewConcerts)

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/notification "Not signed in"
		for _, cfg := range []config.BaseConfig{devConfig(), prodConfig()} {
			h := newAdminNotificationHandler(t, ucmocks.NewMockPushSubscriptionUseCase(t), cfg)

			_, viaPipeline := callThroughAdminPipeline(t, context.Background(), notifyRequest(2), h.NotifyNewConcerts)
			_, direct := h.NotifyNewConcerts(context.Background(), connect.NewRequest(notifyRequest(2)))

			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(viaPipeline), "environment %q", cfg.Environment)
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(direct), "the handler itself checks sign-in before the environment (%q)", cfg.Environment)
		}
	})

	t.Run("caller without the admin role", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/notification "Caller without the admin role"
		h := newAdminNotificationHandler(t, ucmocks.NewMockPushSubscriptionUseCase(t), devConfig())

		_, err := callThroughAdminPipeline(t, nonAdminCtx(), notifyRequest(2), h.NotifyNewConcerts)

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("no concerts", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/notification "No concerts"
		h := newAdminNotificationHandler(t, ucmocks.NewMockPushSubscriptionUseCase(t), devConfig())

		_, viaPipeline := callThroughAdminPipeline(t, adminCtx(), notifyRequest(0), h.NotifyNewConcerts)
		_, direct := h.NotifyNewConcerts(adminCtx(), connect.NewRequest(notifyRequest(0)))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(viaPipeline))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(direct))
	})

	t.Run("too many concerts", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/notification "Too many concerts"
		h := newAdminNotificationHandler(t, ucmocks.NewMockPushSubscriptionUseCase(t), devConfig())

		_, err := callThroughAdminPipeline(t, adminCtx(), notifyRequest(1001), h.NotifyNewConcerts)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	tests := []struct {
		name string
		req  *adminnotificationv1.NotifyNewConcertsRequest
	}{
		{
			name: "return INVALID_ARGUMENT without an artist",
			req:  &adminnotificationv1.NotifyNewConcertsRequest{ConcertIds: []*entitypb.EventId{{Value: "concert-1"}}},
		},
		{
			name: "return INVALID_ARGUMENT for an empty concert id",
			req: &adminnotificationv1.NotifyNewConcertsRequest{
				ArtistId:   &entitypb.ArtistId{Value: "artist-1"},
				ConcertIds: []*entitypb.EventId{{Value: "concert-1"}, {Value: ""}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newAdminNotificationHandler(t, ucmocks.NewMockPushSubscriptionUseCase(t), devConfig())

			_, err := h.NotifyNewConcerts(adminCtx(), connect.NewRequest(tt.req))

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}

	t.Run("return the usecase failure unchanged", func(t *testing.T) {
		t.Parallel()
		uc := ucmocks.NewMockPushSubscriptionUseCase(t)
		uc.EXPECT().NotifyNewConcerts(mock.Anything, mock.Anything).
			Return(apperr.New(apperrcodes.InvalidArgument, "ownership violation")).Once()
		h := newAdminNotificationHandler(t, uc, devConfig())

		_, err := h.NotifyNewConcerts(adminCtx(), connect.NewRequest(notifyRequest(2)))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
