package rpc

import (
	"context"

	adminnotificationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/notification/v1/notificationv1connect"
	adminnotificationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/notification/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/pb"
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
	notificationUC usecase.NotificationUseCase
	isProduction   bool
	logger         *logging.Logger
}

// NewAdminNotificationHandler creates a new AdminNotificationHandler.
func NewAdminNotificationHandler(
	notificationUC usecase.NotificationUseCase,
	cfg config.BaseConfig,
	logger *logging.Logger,
) *AdminNotificationHandler {
	return &AdminNotificationHandler{
		notificationUC: notificationUC,
		isProduction:   cfg.IsProduction(),
		logger:         logger,
	}
}

// NotifyNewConcerts runs the new-concert push delivery for the given artist
// and exactly the given concerts, bypassing the event bus, and returns once it
// completes. An unauthenticated caller fails with Unauthenticated in every
// environment, so it cannot learn the environment; in production every other
// caller fails with PermissionDenied and nothing is sent. Every concert must
// belong to the artist; otherwise the whole request fails with
// InvalidArgument (no partial delivery).
func (h *AdminNotificationHandler) NotifyNewConcerts(ctx context.Context, req *adminnotificationv1.NotifyNewConcertsRequest) (*adminnotificationv1.NotifyNewConcertsResponse, error) {
	if _, err := pb.GetExternalUserID(ctx); err != nil {
		return nil, err
	}
	if h.isProduction {
		return nil, connect.NewError(connect.CodePermissionDenied, "debug RPC is not available in production")
	}

	if req.GetArtistId().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "artist_id is required")
	}
	if len(req.GetConcertIds()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "at least one concert_id is required")
	}

	concertIDs := make([]string, 0, len(req.GetConcertIds()))
	for _, id := range req.GetConcertIds() {
		if id.GetValue() == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, "concert_id values must not be empty")
		}
		concertIDs = append(concertIDs, id.GetValue())
	}

	if err := h.notificationUC.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{
		ArtistID:   req.GetArtistId().GetValue(),
		ConcertIDs: concertIDs,
	}); err != nil {
		return nil, err
	}

	return &adminnotificationv1.NotifyNewConcertsResponse{}, nil
}
