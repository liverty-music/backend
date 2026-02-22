package rpc

import (
	"context"
	"errors"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/push_notification/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// PushNotificationHandler implements the PushNotificationService Connect interface.
type PushNotificationHandler struct {
	pushUseCase usecase.PushNotificationUseCase
	logger      *logging.Logger
}

// NewPushNotificationHandler creates a new instance of the push notification RPC service handler.
func NewPushNotificationHandler(
	pushUseCase usecase.PushNotificationUseCase,
	logger *logging.Logger,
) *PushNotificationHandler {
	return &PushNotificationHandler{
		pushUseCase: pushUseCase,
		logger:      logger,
	}
}

// Subscribe registers or updates the browser push subscription for the authenticated user.
func (h *PushNotificationHandler) Subscribe(ctx context.Context, req *connect.Request[rpc.SubscribeRequest]) (*connect.Response[rpc.SubscribeResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
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

	if err := h.pushUseCase.Subscribe(ctx, userID, req.Msg.Endpoint, req.Msg.P256Dh, req.Msg.Auth); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.SubscribeResponse{}), nil
}

// Unsubscribe removes all push subscriptions associated with the authenticated user.
func (h *PushNotificationHandler) Unsubscribe(ctx context.Context, _ *connect.Request[rpc.UnsubscribeRequest]) (*connect.Response[rpc.UnsubscribeResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if err := h.pushUseCase.Unsubscribe(ctx, userID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.UnsubscribeResponse{}), nil
}
