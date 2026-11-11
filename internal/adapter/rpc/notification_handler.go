package rpc

import (
	"context"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	notificationpb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/notification/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// NotificationHandler implements the NotificationService Connect interface: a
// fan lists their notifications and marks one read.
type NotificationHandler struct {
	notificationUC usecase.NotificationUseCase
	userUC         usecase.UserUseCase
	logger         *logging.Logger
}

// NewNotificationHandler creates a new notification RPC service handler.
func NewNotificationHandler(
	notificationUC usecase.NotificationUseCase,
	userUC usecase.UserUseCase,
	logger *logging.Logger,
) *NotificationHandler {
	return &NotificationHandler{
		notificationUC: notificationUC,
		userUC:         userUC,
		logger:         logger,
	}
}

// List returns the signed-in caller's newest notifications, each rendered in
// their language.
func (h *NotificationHandler) List(ctx context.Context, _ *notificationpb.ListRequest) (*notificationpb.ListResponse, error) {
	user, err := h.resolveCaller(ctx)
	if err != nil {
		return nil, err
	}
	listed, err := h.notificationUC.List(ctx, user)
	if err != nil {
		return nil, err
	}
	notifications := make([]*entityv1.Notification, len(listed))
	for i, l := range listed {
		notifications[i] = pb.FromNotification(l.Notification, l.Message)
	}
	return &notificationpb.ListResponse{Notifications: notifications}, nil
}

// MarkRead marks the signed-in caller's notification as read. The request
// names no fan; NotificationUseCase.MarkRead refuses another fan's
// Notification.
func (h *NotificationHandler) MarkRead(ctx context.Context, req *notificationpb.MarkReadRequest) (*notificationpb.MarkReadResponse, error) {
	user, err := h.resolveCaller(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.notificationUC.MarkRead(ctx, user.ID, req.GetNotificationId().GetValue()); err != nil {
		return nil, err
	}
	return &notificationpb.MarkReadResponse{}, nil
}

// resolveCaller resolves the signed-in caller's stored User from the JWT
// context, to scope the use-case call.
func (h *NotificationHandler) resolveCaller(ctx context.Context) (*entity.User, error) {
	providerUserID, err := pb.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	// notifications.user_id references users.id (internal UUID), not the
	// identity-provider provider_user_id, so resolve the internal record first.
	return h.userUC.ResolveCaller(ctx, providerUserID)
}
