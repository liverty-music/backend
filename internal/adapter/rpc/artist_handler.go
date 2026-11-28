package rpc

import (
	"context"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/artist/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
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
func (h *ArtistHandler) List(ctx context.Context, _ *rpc.ListRequest) (*rpc.ListResponse, error) {
	artists, err := h.artistUseCase.List(ctx)
	if err != nil {
		return nil, err
	}

	var protoArtists []*entityv1.Artist
	for _, a := range artists {
		protoArtists = append(protoArtists, pb.FromArtist(a))
	}

	return &rpc.ListResponse{
		Artists: protoArtists,
	}, nil
}

// Search performs an incremental search for artists by name.
func (h *ArtistHandler) Search(ctx context.Context, req *rpc.SearchRequest) (*rpc.SearchResponse, error) {
	artists, err := h.artistUseCase.Search(ctx, req.Query)
	if err != nil {
		return nil, err
	}

	return &rpc.SearchResponse{
		Artists: pb.FromArtists(artists),
	}, nil
}

// ListSimilar retrieves a collection of artists with musical affinity to a target artist.
func (h *ArtistHandler) ListSimilar(ctx context.Context, req *rpc.ListSimilarRequest) (*rpc.ListSimilarResponse, error) {
	artists, err := h.artistUseCase.ListSimilar(ctx, req.ArtistId.Value, req.GetLimit())
	if err != nil {
		return nil, err
	}

	return &rpc.ListSimilarResponse{
		Artists: pb.FromArtists(artists),
	}, nil
}

// ListTop retrieves a collection of trending or popular artists, optionally filtered by country or genre tag.
func (h *ArtistHandler) ListTop(ctx context.Context, req *rpc.ListTopRequest) (*rpc.ListTopResponse, error) {
	artists, err := h.artistUseCase.ListTop(ctx, req.GetCountry(), req.GetTag(), req.GetLimit())
	if err != nil {
		return nil, err
	}

	return &rpc.ListTopResponse{
		Artists: pb.FromArtists(artists),
	}, nil
}
