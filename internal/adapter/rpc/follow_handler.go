package rpc

import (
	"context"

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
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	err = h.followUseCase.Follow(ctx, user.ID, req.Msg.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.FollowResponse{}), nil
}

// Unfollow removes an existing follow relationship.
func (h *FollowHandler) Unfollow(ctx context.Context, req *connect.Request[rpc.UnfollowRequest]) (*connect.Response[rpc.UnfollowResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	err = h.followUseCase.Unfollow(ctx, user.ID, req.Msg.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.UnfollowResponse{}), nil
}

// ListFollowed retrieves the list of artists currently followed by the authenticated user.
func (h *FollowHandler) ListFollowed(ctx context.Context, _ *connect.Request[rpc.ListFollowedRequest]) (*connect.Response[rpc.ListFollowedResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
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
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	hype := mapper.HypeFromProto[req.Msg.Hype]

	err = h.followUseCase.SetHype(ctx, user.ID, req.Msg.ArtistId.Value, hype)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.SetHypeResponse{}), nil
}
