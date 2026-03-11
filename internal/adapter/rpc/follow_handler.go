package rpc

import (
	"context"
	"errors"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/follow/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// FollowHandler implements the FollowService Connect interface.
type FollowHandler struct {
	followUseCase usecase.FollowUseCase
	logger        *logging.Logger
}

// NewFollowHandler creates a new instance of the follow RPC service handler.
func NewFollowHandler(
	followUseCase usecase.FollowUseCase,
	logger *logging.Logger,
) *FollowHandler {
	return &FollowHandler{
		followUseCase: followUseCase,
		logger:        logger,
	}
}

// Follow establishes a follow relationship between the current user and an artist.
func (h *FollowHandler) Follow(ctx context.Context, req *connect.Request[rpc.FollowRequest]) (*connect.Response[rpc.FollowResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	err := h.followUseCase.Follow(ctx, userID, req.Msg.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.FollowResponse{}), nil
}

// Unfollow removes an existing follow relationship.
func (h *FollowHandler) Unfollow(ctx context.Context, req *connect.Request[rpc.UnfollowRequest]) (*connect.Response[rpc.UnfollowResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	err := h.followUseCase.Unfollow(ctx, userID, req.Msg.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.UnfollowResponse{}), nil
}

// ListFollowed retrieves the list of artists currently followed by the authenticated user.
func (h *FollowHandler) ListFollowed(ctx context.Context, _ *connect.Request[rpc.ListFollowedRequest]) (*connect.Response[rpc.ListFollowedResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	followed, err := h.followUseCase.ListFollowed(ctx, userID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.ListFollowedResponse{
		Artists: mapper.FollowedArtistsToProto(followed),
	}), nil
}

// SetHype updates the user's enthusiasm tier for a followed artist.
func (h *FollowHandler) SetHype(ctx context.Context, req *connect.Request[rpc.SetHypeRequest]) (*connect.Response[rpc.SetHypeResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	hype, ok := mapper.HypeFromProto[req.Msg.Hype]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid hype level"))
	}

	err := h.followUseCase.SetHype(ctx, userID, req.Msg.ArtistId.Value, hype)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.SetHypeResponse{}), nil
}
