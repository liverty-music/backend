package rpc

import (
	"context"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	artistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/artist/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// ArtistHandler implements the ArtistService Connect interface.
type ArtistHandler struct {
	artistUseCase usecase.ArtistUseCase
	logger        *logging.Logger
}

// NewArtistHandler creates a new artist handler.
func NewArtistHandler(
	artistUseCase usecase.ArtistUseCase,
	logger *logging.Logger,
) *ArtistHandler {
	return &ArtistHandler{
		artistUseCase: artistUseCase,
		logger:        logger,
	}
}

// List retrieves a collection of all registered artists in the database.
func (h *ArtistHandler) List(ctx context.Context, _ *connect.Request[artistv1.ListRequest]) (*connect.Response[artistv1.ListResponse], error) {
	artists, err := h.artistUseCase.List(ctx)
	if err != nil {
		return nil, err
	}

	var protoArtists []*entityv1.Artist
	for _, a := range artists {
		protoArtists = append(protoArtists, mapper.ArtistToProto(a))
	}

	return connect.NewResponse(&artistv1.ListResponse{
		Artists: protoArtists,
	}), nil
}

// Create registers a new musical performer or band in the system.
func (h *ArtistHandler) Create(ctx context.Context, req *connect.Request[artistv1.CreateRequest]) (*connect.Response[artistv1.CreateResponse], error) {
	artist := &entity.Artist{
		Name: req.Msg.Name.Value,
	}

	created, err := h.artistUseCase.Create(ctx, artist)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&artistv1.CreateResponse{
		Artist: mapper.ArtistToProto(created),
	}), nil
}

// Search performs an incremental search for artists by name.
func (h *ArtistHandler) Search(ctx context.Context, req *connect.Request[artistv1.SearchRequest]) (*connect.Response[artistv1.SearchResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// CreateMedia associates a new official media channel with an artist.
func (h *ArtistHandler) CreateMedia(ctx context.Context, req *connect.Request[artistv1.CreateMediaRequest]) (*connect.Response[artistv1.CreateMediaResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// DeleteMedia removes an existing media channel association.
func (h *ArtistHandler) DeleteMedia(ctx context.Context, req *connect.Request[artistv1.DeleteMediaRequest]) (*connect.Response[artistv1.DeleteMediaResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// Follow establishes a follow relationship between the current user and an artist.
func (h *ArtistHandler) Follow(ctx context.Context, req *connect.Request[artistv1.FollowRequest]) (*connect.Response[artistv1.FollowResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// Unfollow removes an existing follow relationship.
func (h *ArtistHandler) Unfollow(ctx context.Context, req *connect.Request[artistv1.UnfollowRequest]) (*connect.Response[artistv1.UnfollowResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// ListFollowed retrieves the list of artists currently followed by the authenticated user.
func (h *ArtistHandler) ListFollowed(ctx context.Context, req *connect.Request[artistv1.ListFollowedRequest]) (*connect.Response[artistv1.ListFollowedResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// ListSimilar retrieves artists similar to a specified artist.
func (h *ArtistHandler) ListSimilar(ctx context.Context, req *connect.Request[artistv1.ListSimilarRequest]) (*connect.Response[artistv1.ListSimilarResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// ListTop retrieves globally or regionally popular artists.
func (h *ArtistHandler) ListTop(ctx context.Context, req *connect.Request[artistv1.ListTopRequest]) (*connect.Response[artistv1.ListTopResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
