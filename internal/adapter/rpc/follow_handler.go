package rpc

import (
	"context"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/follow/v1"
	"connectrpc.com/connect/v2"
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
func (h *FollowHandler) Follow(ctx context.Context, req *rpc.FollowRequest) (*rpc.FollowResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	err = h.followUseCase.Follow(ctx, user.ID, req.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return &rpc.FollowResponse{}, nil
}

// Unfollow removes an existing follow relationship.
func (h *FollowHandler) Unfollow(ctx context.Context, req *rpc.UnfollowRequest) (*rpc.UnfollowResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	err = h.followUseCase.Unfollow(ctx, user.ID, req.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return &rpc.UnfollowResponse{}, nil
}

// List returns the signed-in fan's Follows with their Artists.
func (h *FollowHandler) List(ctx context.Context, _ *rpc.ListRequest) (*rpc.ListResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	followed, err := h.followUseCase.List(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	return &rpc.ListResponse{
		Artists: mapper.FollowedArtistsToProto(followed),
	}, nil
}

// SetHype updates the user's enthusiasm tier for a followed artist.
func (h *FollowHandler) SetHype(ctx context.Context, req *rpc.SetHypeRequest) (*rpc.SetHypeResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	// Reject HYPE_TYPE_UNSPECIFIED (proto3 zero value) explicitly rather than
	// letting it fall through to entity.Hype("") and surface as an internal
	// DB constraint error. With the per-follow default flipped to Nearby
	// (opt-in to notifications), an unintentional SetHype call carries more
	// weight than under the previous Watch default — fail loud.
	if req.Hype == entityv1.HypeType_HYPE_TYPE_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, "hype is required")
	}

	hype := mapper.HypeFromProto[req.Hype]

	err = h.followUseCase.SetHype(ctx, user.ID, req.ArtistId.Value, hype)
	if err != nil {
		return nil, err
	}

	return &rpc.SetHypeResponse{}, nil
}
