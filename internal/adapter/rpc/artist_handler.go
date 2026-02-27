package rpc

import (
	"context"
	"errors"

	pb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/artist/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// ArtistHandler implements the ArtistService Connect interface.
type ArtistHandler struct {
	artistUseCase usecase.ArtistUseCase
	logger        *logging.Logger
}

// NewArtistHandler creates a new instance of the artist RPC service handler.
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
func (h *ArtistHandler) List(ctx context.Context, _ *connect.Request[rpc.ListRequest]) (*connect.Response[rpc.ListResponse], error) {
	artists, err := h.artistUseCase.List(ctx)
	if err != nil {
		return nil, err
	}

	var protoArtists []*pb.Artist
	for _, a := range artists {
		protoArtists = append(protoArtists, mapper.ArtistToProto(a))
	}

	return connect.NewResponse(&rpc.ListResponse{
		Artists: protoArtists,
	}), nil
}

// Create registers a new musical performer or band in the system.
func (h *ArtistHandler) Create(ctx context.Context, req *connect.Request[rpc.CreateRequest]) (*connect.Response[rpc.CreateResponse], error) {
	if req.Msg.Name == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	var mbid string
	if req.Msg.Mbid != nil {
		mbid = req.Msg.Mbid.Value
	}
	artist := entity.NewArtist(req.Msg.Name.Value, mbid)

	created, err := h.artistUseCase.Create(ctx, artist)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.CreateResponse{
		Artist: mapper.ArtistToProto(created),
	}), nil
}

// Search performs an incremental search for artists by name.
func (h *ArtistHandler) Search(ctx context.Context, req *connect.Request[rpc.SearchRequest]) (*connect.Response[rpc.SearchResponse], error) {
	artists, err := h.artistUseCase.Search(ctx, req.Msg.Query)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.SearchResponse{
		Artists: mapper.ArtistsToProto(artists),
	}), nil
}

// CreateOfficialSite associates a new official website or social media channel with an artist.
func (h *ArtistHandler) CreateOfficialSite(ctx context.Context, req *connect.Request[rpc.CreateOfficialSiteRequest]) (*connect.Response[rpc.CreateOfficialSiteResponse], error) {
	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}
	if req.Msg.Url == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("url is required"))
	}

	err := h.artistUseCase.CreateOfficialSite(ctx, &entity.OfficialSite{
		ArtistID: req.Msg.ArtistId.Value,
		URL:      req.Msg.Url.Value,
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.CreateOfficialSiteResponse{}), nil
}

// DeleteOfficialSite removes an existing official site association.
func (h *ArtistHandler) DeleteOfficialSite(ctx context.Context, req *connect.Request[rpc.DeleteOfficialSiteRequest]) (*connect.Response[rpc.DeleteOfficialSiteResponse], error) {
	// TODO: Implement DeleteOfficialSite in UseCase
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("DeleteOfficialSite is not implemented"))
}

// Follow establishes a follow relationship between the current user and an artist.
func (h *ArtistHandler) Follow(ctx context.Context, req *connect.Request[rpc.FollowRequest]) (*connect.Response[rpc.FollowResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	err := h.artistUseCase.Follow(ctx, userID, req.Msg.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.FollowResponse{}), nil
}

// Unfollow removes an existing follow relationship.
func (h *ArtistHandler) Unfollow(ctx context.Context, req *connect.Request[rpc.UnfollowRequest]) (*connect.Response[rpc.UnfollowResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	err := h.artistUseCase.Unfollow(ctx, userID, req.Msg.ArtistId.Value)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.UnfollowResponse{}), nil
}

// ListFollowed retrieves the list of artists currently followed by the authenticated user.
func (h *ArtistHandler) ListFollowed(ctx context.Context, req *connect.Request[rpc.ListFollowedRequest]) (*connect.Response[rpc.ListFollowedResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	followed, err := h.artistUseCase.ListFollowed(ctx, userID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.ListFollowedResponse{
		Artists: mapper.FollowedArtistsToProto(followed),
	}), nil
}

// SetPassionLevel updates the user's enthusiasm tier for a followed artist.
func (h *ArtistHandler) SetPassionLevel(ctx context.Context, req *connect.Request[rpc.SetPassionLevelRequest]) (*connect.Response[rpc.SetPassionLevelResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	level, ok := mapper.PassionLevelFromProto[req.Msg.PassionLevel]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid passion level"))
	}

	err := h.artistUseCase.SetPassionLevel(ctx, userID, req.Msg.ArtistId.Value, level)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.SetPassionLevelResponse{}), nil
}

// ListSimilar retrieves a collection of artists with musical affinity to a target artist.
func (h *ArtistHandler) ListSimilar(ctx context.Context, req *connect.Request[rpc.ListSimilarRequest]) (*connect.Response[rpc.ListSimilarResponse], error) {
	if req.Msg.ArtistId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artist_id is required"))
	}

	artists, err := h.artistUseCase.ListSimilar(ctx, req.Msg.ArtistId.Value, req.Msg.GetLimit())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.ListSimilarResponse{
		Artists: mapper.ArtistsToProto(artists),
	}), nil
}

// ListTop retrieves a collection of trending or popular artists, optionally filtered by country or genre tag.
func (h *ArtistHandler) ListTop(ctx context.Context, req *connect.Request[rpc.ListTopRequest]) (*connect.Response[rpc.ListTopResponse], error) {
	artists, err := h.artistUseCase.ListTop(ctx, req.Msg.GetCountry(), req.Msg.GetTag(), req.Msg.GetLimit())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.ListTopResponse{
		Artists: mapper.ArtistsToProto(artists),
	}), nil
}
