package rpc

import (
	"context"
	"errors"

	entitypb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/push_notification/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// PushNotificationHandler implements the PushNotificationService Connect interface.
type PushNotificationHandler struct {
	pushUseCase  usecase.PushNotificationUseCase
	userRepo     entity.UserRepository
	isProduction bool
	logger       *logging.Logger
}

// NewPushNotificationHandler creates a new instance of the push notification RPC service handler.
func NewPushNotificationHandler(
	pushUseCase usecase.PushNotificationUseCase,
	userRepo entity.UserRepository,
	cfg config.BaseConfig,
	logger *logging.Logger,
) *PushNotificationHandler {
	return &PushNotificationHandler{
		pushUseCase:  pushUseCase,
		userRepo:     userRepo,
		isProduction: cfg.IsProduction(),
		logger:       logger,
	}
}

// Create registers the calling browser's push subscription for the authenticated user.
// The user identity is always resolved from the JWT context — clients do not
// supply a user_id for creation because subscriptions are owned by the
// authenticated caller by construction.
func (h *PushNotificationHandler) Create(ctx context.Context, req *connect.Request[rpc.CreateRequest]) (*connect.Response[rpc.CreateResponse], error) {
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

	sub, err := h.pushUseCase.Create(ctx, user.ID, endpoint, p256dh, auth)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.CreateResponse{
		Subscription: toPushSubscriptionProto(sub),
	}), nil
}

// Get retrieves the push subscription identified by (user_id, endpoint). The
// request-supplied user_id is verified against the JWT-derived userID;
// mismatches are rejected with PERMISSION_DENIED. Returns NOT_FOUND when no
// subscription exists for the (user_id, endpoint) pair.
func (h *PushNotificationHandler) Get(ctx context.Context, req *connect.Request[rpc.GetRequest]) (*connect.Response[rpc.GetResponse], error) {
	user, err := h.resolveCallerUser(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.GetUserId() == nil || req.Msg.GetEndpoint() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id and endpoint are required"))
	}
	if err := mapper.RequireUserIDMatch(user.ID, req.Msg.GetUserId().GetValue()); err != nil {
		return nil, err
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

// Delete removes the push subscription identified by (user_id, endpoint). The
// request-supplied user_id is verified against the JWT-derived userID;
// mismatches are rejected with PERMISSION_DENIED. Only the specified
// browser's subscription is removed.
func (h *PushNotificationHandler) Delete(ctx context.Context, req *connect.Request[rpc.DeleteRequest]) (*connect.Response[rpc.DeleteResponse], error) {
	user, err := h.resolveCallerUser(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.GetUserId() == nil || req.Msg.GetEndpoint() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id and endpoint are required"))
	}
	if err := mapper.RequireUserIDMatch(user.ID, req.Msg.GetUserId().GetValue()); err != nil {
		return nil, err
	}
	endpoint := req.Msg.GetEndpoint().GetValue()

	if err := h.pushUseCase.Delete(ctx, user.ID, endpoint); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.DeleteResponse{}), nil
}

// NotifyNewConcerts triggers the push notification delivery pipeline for the
// supplied artist + concert_ids, bypassing the NATS event bus. This RPC is
// restricted to non-production environments — production returns
// PERMISSION_DENIED. Every supplied concert_id must belong to the artist;
// otherwise the entire request is rejected with INVALID_ARGUMENT (no partial
// delivery).
func (h *PushNotificationHandler) NotifyNewConcerts(ctx context.Context, req *connect.Request[rpc.NotifyNewConcertsRequest]) (*connect.Response[rpc.NotifyNewConcertsResponse], error) {
	if h.isProduction {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("debug RPC is not available in production"))
	}
	// Require a valid authenticated session even in non-production so callers
	// get UNAUTHENTICATED (not PERMISSION_DENIED) when unauthenticated.
	if _, err := mapper.GetExternalUserID(ctx); err != nil {
		return nil, err
	}

	if req.Msg.GetArtistId() == nil || req.Msg.GetArtistId().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}
	if len(req.Msg.GetConcertIds()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one concert_id is required"))
	}

	concertIDs := make([]string, 0, len(req.Msg.GetConcertIds()))
	for _, id := range req.Msg.GetConcertIds() {
		if id == nil || id.GetValue() == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("concert_id values must not be empty"))
		}
		concertIDs = append(concertIDs, id.GetValue())
	}

	// Delegate ownership validation and delivery to the use case. Any
	// apperr InvalidArgument (e.g., concert_id ownership mismatch) returned
	// from the use case is translated to connect.CodeInvalidArgument by
	// the error-handling interceptor.
	if err := h.pushUseCase.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{
		ArtistID:   req.Msg.GetArtistId().GetValue(),
		ConcertIDs: concertIDs,
	}); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.NotifyNewConcertsResponse{}), nil
}

// resolveCallerUser extracts the external_id from JWT claims and resolves the
// internal user record for the caller.
func (h *PushNotificationHandler) resolveCallerUser(ctx context.Context) (*entity.User, error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	// push_subscriptions.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id, so we must resolve
	// the internal user record before interacting with the repository.
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// toPushSubscriptionProto maps the internal entity to the wire type returned
// by Create and Get RPCs.
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
