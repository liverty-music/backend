package rpc

import (
	"context"
	"errors"

	pushsubscriptionv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/push_subscription/v1/push_subscriptionv1connect"
	entitypb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/push_subscription/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/pb"
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
	pushUseCase usecase.PushSubscriptionUseCase
	userRepo    entity.UserRepository
	logger      *logging.Logger
}

// NewPushSubscriptionHandler creates a new PushSubscriptionHandler.
func NewPushSubscriptionHandler(
	pushUseCase usecase.PushSubscriptionUseCase,
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
func (h *PushSubscriptionHandler) Create(ctx context.Context, req *rpc.CreateRequest) (*rpc.CreateResponse, error) {
	user, err := h.resolveCallerUser(ctx)
	if err != nil {
		return nil, err
	}

	if req.GetEndpoint() == nil || req.GetKeys() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "endpoint and keys are required")
	}
	endpoint := req.GetEndpoint().GetValue()
	p256dh := req.GetKeys().GetP256Dh()
	auth := req.GetKeys().GetAuth()

	sub, err := h.pushUseCase.Create(ctx, user.ID, endpoint, p256dh, auth, pb.DeviceTypeFromEndpoint(endpoint))
	if err != nil {
		return nil, err
	}

	return &rpc.CreateResponse{
		Subscription: toPushSubscriptionProto(sub),
	}, nil
}

// Get returns the caller's push subscription for the given endpoint, the
// subscription's natural key. It fails with NotFound for an endpoint the
// caller has not registered, whoever else has.
func (h *PushSubscriptionHandler) Get(ctx context.Context, req *rpc.GetRequest) (*rpc.GetResponse, error) {
	user, err := h.resolveCallerUser(ctx)
	if err != nil {
		return nil, err
	}

	if req.GetEndpoint() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "endpoint is required")
	}
	endpoint := req.GetEndpoint().GetValue()

	sub, err := h.pushUseCase.Get(ctx, user.ID, endpoint)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "push subscription not found")
		}
		return nil, err
	}

	return &rpc.GetResponse{
		Subscription: toPushSubscriptionProto(sub),
	}, nil
}

// Delete removes the caller's push subscription for the given endpoint. Only
// that browser's subscription of the caller is removed; an endpoint only
// another fan has registered removes nothing.
func (h *PushSubscriptionHandler) Delete(ctx context.Context, req *rpc.DeleteRequest) (*rpc.DeleteResponse, error) {
	user, err := h.resolveCallerUser(ctx)
	if err != nil {
		return nil, err
	}

	if req.GetEndpoint() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "endpoint is required")
	}
	endpoint := req.GetEndpoint().GetValue()

	if err := h.pushUseCase.Delete(ctx, user.ID, endpoint, pb.DeviceTypeFromEndpoint(endpoint)); err != nil {
		return nil, err
	}

	return &rpc.DeleteResponse{}, nil
}

// resolveCallerUser extracts the provider_user_id from JWT claims and resolves the
// internal user record for the caller.
func (h *PushSubscriptionHandler) resolveCallerUser(ctx context.Context) (*entity.User, error) {
	providerUserID, err := pb.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	// push_subscriptions.user_id references users.id (internal UUID),
	// not the identity-provider-specific provider_user_id, so we must resolve
	// the internal user record before interacting with the repository.
	return h.userRepo.GetByProviderUserID(ctx, providerUserID)
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
