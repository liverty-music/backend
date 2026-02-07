// Package rpc implements Connect-RPC handlers for the application's gRPC services.
package rpc

import (
	"context"
	"fmt"

	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// ConcertHandler implements the ConcertService Connect interface.
type ConcertHandler struct {
	concertUseCase usecase.ConcertUseCase
	logger         *logging.Logger
}

// NewConcertHandler creates a new concert handler.
func NewConcertHandler(
	concertUseCase usecase.ConcertUseCase,
	logger *logging.Logger,
) *ConcertHandler {
	return &ConcertHandler{
		concertUseCase: concertUseCase,
		logger:         logger,
	}
}

// List returns a list of concerts, optionally filtered by artist.
func (h *ConcertHandler) List(ctx context.Context, req *connect.Request[concertv1.ListRequest]) (*connect.Response[concertv1.ListResponse], error) {
	artistID := req.Msg.GetArtistId().GetValue()

	concerts, err := h.concertUseCase.ListByArtist(ctx, artistID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.ListResponse{
		Concerts: mapper.ConcertsToProto(concerts),
	}), nil
}

// SearchNewConcerts triggers a discovery process for new concerts.
func (h *ConcertHandler) SearchNewConcerts(ctx context.Context, req *connect.Request[concertv1.SearchNewConcertsRequest]) (*connect.Response[concertv1.SearchNewConcertsResponse], error) {
	artistID := req.Msg.GetArtistId().GetValue()
	if artistID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("artist_id cannot be empty"))
	}

	concerts, err := h.concertUseCase.SearchNewConcerts(ctx, artistID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.SearchNewConcertsResponse{
		Concerts: mapper.ConcertsToProto(concerts),
	}), nil
}
