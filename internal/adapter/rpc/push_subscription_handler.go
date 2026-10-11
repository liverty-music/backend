package rpc

import (
	"context"
	"errors"

	pushsubscriptionv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/push_subscription/v1/push_subscriptionv1connect"
	entitypb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/push_subscription/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that PushSubscriptionHandler satisfies the generated
// interface.
var _ pushsubscriptionv1connect.PushSubscriptionServiceHandler = (*PushSubscriptionHandler)(nil)

// PushSubscriptionHandler implements the fan-facing PushSubscriptionService
// Connect interface. Every call acts on the signed-in caller's own browsers'
// push subscriptions; no request names a user.
type PushSubscriptionHandler struct {
	pushUseCase usecase.PushNotificationUseCase
	userRepo    entity.UserRepository
	logger      *logging.Logger
}

// NewPushSubscriptionHandler creates a new PushSubscriptionHandler.
func NewPushSubscriptionHandler(
	pushUseCase usecase.PushNotificationUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *PushSubscriptionHandler {
	return &PushSubscriptionHandler{
		pushUseCase: pushUseCase,
		userRepo:    userRepo,
		logger:      logger,
	}
}

// Create registers the calling browser's push subscription for the signed-in
// caller.
func (h *PushSubscriptionHandler) Create(ctx context.Context, req *connect.Request[rpc.CreateRequest]) (*connect.Response[rpc.CreateResponse], error) {
	user, err := h.resolveCallerUser(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.GetEndpoint() == nil || req.Msg.GetKeys() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("endpoint and keys are required"))
	}
	endpoint := req.Msg.GetEndpoint().GetValue()
	p256dh := req.Msg.GetKeys().GetP256Dh()
	auth := req.Msg.GetKeys().GetAuth()

	sub, err := h.pushUseCase.Create(ctx, user.ID, endpoint, p256dh, auth, mapper.DeviceTypeFromEndpoint(endpoint))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.CreateResponse{
		Subscription: toPushSubscriptionProto(sub),
	}), nil
}

// Get returns the caller's push subscription for the given endpoint, the
// subscription's natural key. It fails with NotFound for an endpoint the
// caller has not registered, whoever else has.
func (h *PushSubscriptionHandler) Get(ctx context.Context, req *connect.Request[rpc.GetRequest]) (*connect.Response[rpc.GetResponse], error) {
	user, err := h.resolveCallerUser(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.GetEndpoint() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("endpoint is required"))
	}
	endpoint := req.Msg.GetEndpoint().GetValue()

	sub, err := h.pushUseCase.Get(ctx, user.ID, endpoint)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("push subscription not found"))
		}
		return nil, err
	}

	return connect.NewResponse(&rpc.GetResponse{
		Subscription: toPushSubscriptionProto(sub),
	}), nil
}

// Delete removes the caller's push subscription for the given endpoint. Only
// that browser's subscription of the caller is removed; an endpoint only
// another fan has registered removes nothing.
func (h *PushSubscriptionHandler) Delete(ctx context.Context, req *connect.Request[rpc.DeleteRequest]) (*connect.Response[rpc.DeleteResponse], error) {
	user, err := h.resolveCallerUser(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.GetEndpoint() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("endpoint is required"))
	}
	endpoint := req.Msg.GetEndpoint().GetValue()

	if err := h.pushUseCase.Delete(ctx, user.ID, endpoint, mapper.DeviceTypeFromEndpoint(endpoint)); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.DeleteResponse{}), nil
}

// resolveCallerUser extracts the external_id from JWT claims and resolves the
// internal user record for the caller.
func (h *PushSubscriptionHandler) resolveCallerUser(ctx context.Context) (*entity.User, error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	// push_subscriptions.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id, so we must resolve
	// the internal user record before interacting with the repository.
	return h.userRepo.GetByExternalID(ctx, externalID)
}

// toPushSubscriptionProto maps the internal entity to the wire type returned
// by Create and Get.
func toPushSubscriptionProto(sub *entity.PushSubscription) *entitypb.PushSubscription {
	return &entitypb.PushSubscription{
		Id:       &entitypb.PushSubscriptionId{Value: sub.ID},
		UserId:   &entitypb.UserId{Value: sub.UserID},
		Endpoint: &entitypb.PushEndpoint{Value: sub.Endpoint},
		Keys: &entitypb.PushKeys{
			P256Dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}
}
