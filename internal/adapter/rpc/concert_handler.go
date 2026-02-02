// Package rpc implements Connect-RPC handlers for the application's gRPC services.
package rpc

import (
	"context"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpcv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// ConcertHandler implements the ConcertService Connect interface.
type ConcertHandler struct {
	artistUseCase  usecase.ArtistUseCase
	concertUseCase usecase.ConcertUseCase
	logger         *logging.Logger
}

// NewConcertHandler creates a new concert handler.
func NewConcertHandler(
	artistUseCase usecase.ArtistUseCase,
	concertUseCase usecase.ConcertUseCase,
	logger *logging.Logger,
) *ConcertHandler {
	return &ConcertHandler{
		artistUseCase:  artistUseCase,
		concertUseCase: concertUseCase,
		logger:         logger,
	}
}

// List returns a list of concerts, optionally filtered by artist.
func (h *ConcertHandler) List(ctx context.Context, req *connect.Request[rpcv1.ListRequest]) (*connect.Response[rpcv1.ListResponse], error) {
	artistID := ""
	if req.Msg.ArtistId != nil {
		artistID = req.Msg.ArtistId.Value
	}

	concerts, err := h.concertUseCase.ListByArtist(ctx, artistID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpcv1.ListResponse{
		Concerts: mapper.ConcertsToProto(concerts),
	}), nil
}

// ListArtists returns a list of all artists.
func (h *ConcertHandler) ListArtists(ctx context.Context, _ *connect.Request[rpcv1.ListArtistsRequest]) (*connect.Response[rpcv1.ListArtistsResponse], error) {
	artists, err := h.artistUseCase.List(ctx)
	if err != nil {
		return nil, err
	}

	var protoArtists []*entityv1.Artist
	for _, a := range artists {
		protoArtists = append(protoArtists, mapper.ArtistToProto(a))
	}

	return connect.NewResponse(&rpcv1.ListArtistsResponse{
		Artists: protoArtists,
	}), nil
}

// CreateArtist creates a new artist.
func (h *ConcertHandler) CreateArtist(ctx context.Context, req *connect.Request[rpcv1.CreateArtistRequest]) (*connect.Response[rpcv1.CreateArtistResponse], error) {
	artist := &entity.Artist{
		Name: req.Msg.Name.Value,
	}

	created, err := h.artistUseCase.Create(ctx, artist)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpcv1.CreateArtistResponse{
		Artist: mapper.ArtistToProto(created),
	}), nil
}

// CreateArtistMedia is a placeholder (deprecated).
func (h *ConcertHandler) CreateArtistMedia(ctx context.Context, req *connect.Request[rpcv1.CreateArtistMediaRequest]) (*connect.Response[rpcv1.CreateArtistMediaResponse], error) {
	return connect.NewResponse(&rpcv1.CreateArtistMediaResponse{}), nil
}

// DeleteArtistMedia is a placeholder (deprecated).
func (h *ConcertHandler) DeleteArtistMedia(ctx context.Context, req *connect.Request[rpcv1.DeleteArtistMediaRequest]) (*connect.Response[rpcv1.DeleteArtistMediaResponse], error) {
	return connect.NewResponse(&rpcv1.DeleteArtistMediaResponse{}), nil
}
