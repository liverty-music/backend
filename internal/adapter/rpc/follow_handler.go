package rpc

import (
	"context"
	"errors"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/follow/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// FollowHandler implements the FollowService Connect interface.
type FollowHandler struct {
	followUseCase usecase.FollowUseCase
	userRepo      entity.UserRepository
	logger        *logging.Logger
}

// NewFollowHandler creates a new instance of the follow RPC service handler.
func NewFollowHandler(
	followUseCase usecase.FollowUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *FollowHandler {
	return &FollowHandler{
		followUseCase: followUseCase,
		userRepo:      userRepo,
		logger:        logger,
	}
}

// Follow establishes a follow relationship between the current user and an artist.
func (h *FollowHandler) Follow(ctx context.Context, req *connect.Request[rpc.FollowRequest]) (*connect.Response[rpc.FollowResponse], error) {
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	// follows.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id.
	user, err := h.userRepo.GetByExternalID(ctx, claims.Sub)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}

	err = h.followUseCase.Follow(ctx, user.ID, req.Msg.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.FollowResponse{}), nil
}

// Unfollow removes an existing follow relationship.
func (h *FollowHandler) Unfollow(ctx context.Context, req *connect.Request[rpc.UnfollowRequest]) (*connect.Response[rpc.UnfollowResponse], error) {
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	// follows.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id.
	user, err := h.userRepo.GetByExternalID(ctx, claims.Sub)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}

	err = h.followUseCase.Unfollow(ctx, user.ID, req.Msg.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.UnfollowResponse{}), nil
}

// ListFollowed retrieves the list of artists currently followed by the authenticated user.
func (h *FollowHandler) ListFollowed(ctx context.Context, _ *connect.Request[rpc.ListFollowedRequest]) (*connect.Response[rpc.ListFollowedResponse], error) {
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	// follows.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id.
	user, err := h.userRepo.GetByExternalID(ctx, claims.Sub)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}

	followed, err := h.followUseCase.ListFollowed(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.ListFollowedResponse{
		Artists: mapper.FollowedArtistsToProto(followed),
	}), nil
}

// SetHype updates the user's enthusiasm tier for a followed artist.
func (h *FollowHandler) SetHype(ctx context.Context, req *connect.Request[rpc.SetHypeRequest]) (*connect.Response[rpc.SetHypeResponse], error) {
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	hype, ok := mapper.HypeFromProto[req.Msg.Hype]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid hype level"))
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	// follows.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id.
	user, err := h.userRepo.GetByExternalID(ctx, claims.Sub)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}

	err = h.followUseCase.SetHype(ctx, user.ID, req.Msg.ArtistId.Value, hype)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.SetHypeResponse{}), nil
}
