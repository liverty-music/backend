package rpc

import (
	"context"
	"errors"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/push_notification/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// PushNotificationHandler implements the PushNotificationService Connect interface.
type PushNotificationHandler struct {
	pushUseCase usecase.PushNotificationUseCase
	userRepo    entity.UserRepository
	logger      *logging.Logger
}

// NewPushNotificationHandler creates a new instance of the push notification RPC service handler.
func NewPushNotificationHandler(
	pushUseCase usecase.PushNotificationUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *PushNotificationHandler {
	return &PushNotificationHandler{
		pushUseCase: pushUseCase,
		userRepo:    userRepo,
		logger:      logger,
	}
}

// Subscribe registers or updates the browser push subscription for the authenticated user.
func (h *PushNotificationHandler) Subscribe(ctx context.Context, req *connect.Request[rpc.SubscribeRequest]) (*connect.Response[rpc.SubscribeResponse], error) {
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.Endpoint == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("endpoint is required"))
	}
	if req.Msg.P256Dh == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("p256dh is required"))
	}
	if req.Msg.Auth == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("auth is required"))
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	// push_subscriptions.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id.
	user, err := h.userRepo.GetByExternalID(ctx, claims.Sub)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}

	if err := h.pushUseCase.Subscribe(ctx, user.ID, req.Msg.Endpoint, req.Msg.P256Dh, req.Msg.Auth); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.SubscribeResponse{}), nil
}

// Unsubscribe removes all push subscriptions associated with the authenticated user.
func (h *PushNotificationHandler) Unsubscribe(ctx context.Context, _ *connect.Request[rpc.UnsubscribeRequest]) (*connect.Response[rpc.UnsubscribeResponse], error) {
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByExternalID(ctx, claims.Sub)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}

	if err := h.pushUseCase.Unsubscribe(ctx, user.ID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.UnsubscribeResponse{}), nil
}
