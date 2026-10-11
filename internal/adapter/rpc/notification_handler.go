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

// MarkRead marks the signed-in caller's notification as read. The request
// names no fan; NotificationUseCase.MarkRead refuses another fan's
// Notification.
func (h *NotificationHandler) MarkRead(ctx context.Context, req *connect.Request[notificationpb.MarkReadRequest]) (*connect.Response[notificationpb.MarkReadResponse], error) {
	userID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.notificationUC.MarkRead(ctx, userID, req.Msg.GetNotificationId().GetValue()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&notificationpb.MarkReadResponse{}), nil
}

// Dismiss marks the signed-in caller's notification as dismissed, with the
// same caller scoping as MarkRead.
func (h *NotificationHandler) Dismiss(ctx context.Context, req *connect.Request[notificationpb.DismissRequest]) (*connect.Response[notificationpb.DismissResponse], error) {
	userID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.notificationUC.MarkDismissed(ctx, userID, req.Msg.GetNotificationId().GetValue()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&notificationpb.DismissResponse{}), nil
}

// resolveCallerUserID resolves the signed-in caller's internal user ID from
// the JWT context, to scope the use-case call.
func (h *NotificationHandler) resolveCallerUserID(ctx context.Context) (string, error) {
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
	return user.ID, nil
}
