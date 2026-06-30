package rpc

import (
	"context"

	notificationpb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/notification/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// NotificationHandler implements the NotificationService Connect interface,
// exposing the user-controllable read/dismiss state transitions.
type NotificationHandler struct {
	notificationUC usecase.NotificationUseCase
	userRepo       entity.UserRepository
	logger         *logging.Logger
}

// NewNotificationHandler creates a new notification RPC service handler.
func NewNotificationHandler(
	notificationUC usecase.NotificationUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *NotificationHandler {
	return &NotificationHandler{
		notificationUC: notificationUC,
		userRepo:       userRepo,
		logger:         logger,
	}
}

// MarkRead marks the caller's notification as read. The request-supplied user_id
// is verified against the JWT-derived userID (rpc-auth-scoping convention);
// mismatches are rejected with PERMISSION_DENIED before any state changes.
func (h *NotificationHandler) MarkRead(ctx context.Context, req *connect.Request[notificationpb.MarkReadRequest]) (*connect.Response[notificationpb.MarkReadResponse], error) {
	userID, err := h.authorize(ctx, req.Msg.GetUserId().GetValue())
	if err != nil {
		return nil, err
	}

	if err := h.notificationUC.MarkRead(ctx, userID, req.Msg.GetNotificationId().GetValue()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&notificationpb.MarkReadResponse{}), nil
}

// MarkDismissed marks the caller's notification as dismissed, with the same
// user-scoping guarantees as MarkRead.
func (h *NotificationHandler) MarkDismissed(ctx context.Context, req *connect.Request[notificationpb.MarkDismissedRequest]) (*connect.Response[notificationpb.MarkDismissedResponse], error) {
	userID, err := h.authorize(ctx, req.Msg.GetUserId().GetValue())
	if err != nil {
		return nil, err
	}

	if err := h.notificationUC.MarkDismissed(ctx, userID, req.Msg.GetNotificationId().GetValue()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&notificationpb.MarkDismissedResponse{}), nil
}

// authorize resolves the caller's internal user ID from the JWT context and
// verifies the request-supplied user_id matches it, returning the internal user
// ID to scope the use-case call.
func (h *NotificationHandler) authorize(ctx context.Context, reqUserID string) (string, error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return "", err
	}
	// notifications.user_id references users.id (internal UUID), not the
	// identity-provider external_id, so resolve the internal record first.
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return "", err
	}
	if err := mapper.RequireUserIDMatch(user.ID, reqUserID); err != nil {
		return "", err
	}
	return user.ID, nil
}
