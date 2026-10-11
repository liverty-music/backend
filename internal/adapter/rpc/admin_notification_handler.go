package rpc

import (
	"context"
	"errors"

	adminnotificationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/notification/v1/notificationv1connect"
	adminnotificationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/notification/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that AdminNotificationHandler satisfies the generated
// interface.
var _ adminnotificationv1connect.NotificationServiceHandler = (*AdminNotificationHandler)(nil)

// AdminNotificationHandler implements the admin NotificationService Connect
// interface: a test hook that sends new-concert pushes directly, outside
// production only. The admin role is enforced at the admin server boundary by
// its server-wide RequireRoleInterceptor.
type AdminNotificationHandler struct {
	pushUseCase  usecase.PushNotificationUseCase
	isProduction bool
	logger       *logging.Logger
}

// NewAdminNotificationHandler creates a new AdminNotificationHandler.
func NewAdminNotificationHandler(
	pushUseCase usecase.PushNotificationUseCase,
	cfg config.BaseConfig,
	logger *logging.Logger,
) *AdminNotificationHandler {
	return &AdminNotificationHandler{
		pushUseCase:  pushUseCase,
		isProduction: cfg.IsProduction(),
		logger:       logger,
	}
}

// NotifyNewConcerts runs the new-concert push delivery for the given artist
// and exactly the given concerts, bypassing the event bus, and returns once it
// completes. An unauthenticated caller fails with Unauthenticated in every
// environment, so it cannot learn the environment; in production every other
// caller fails with PermissionDenied and nothing is sent. Every concert must
// belong to the artist; otherwise the whole request fails with
// InvalidArgument (no partial delivery).
func (h *AdminNotificationHandler) NotifyNewConcerts(ctx context.Context, req *connect.Request[adminnotificationv1.NotifyNewConcertsRequest]) (*connect.Response[adminnotificationv1.NotifyNewConcertsResponse], error) {
	if _, err := mapper.GetExternalUserID(ctx); err != nil {
		return nil, err
	}
	if h.isProduction {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("debug RPC is not available in production"))
	}

	if req.Msg.GetArtistId().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}
	if len(req.Msg.GetConcertIds()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one concert_id is required"))
	}

	concertIDs := make([]string, 0, len(req.Msg.GetConcertIds()))
	for _, id := range req.Msg.GetConcertIds() {
		if id.GetValue() == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("concert_id values must not be empty"))
		}
		concertIDs = append(concertIDs, id.GetValue())
	}

	if err := h.pushUseCase.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{
		ArtistID:   req.Msg.GetArtistId().GetValue(),
		ConcertIDs: concertIDs,
	}); err != nil {
		return nil, err
	}

	return connect.NewResponse(&adminnotificationv1.NotifyNewConcertsResponse{}), nil
}
