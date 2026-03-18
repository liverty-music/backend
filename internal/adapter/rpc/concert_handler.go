// Package rpc implements Connect-RPC handlers for the application's gRPC services.
package rpc

import (
	"context"
	"errors"
	"fmt"

	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
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
	artistID := ""
	if req.Msg.ArtistId != nil {
		artistID = req.Msg.ArtistId.Value
	}

	concerts, err := h.concertUseCase.ListByArtist(ctx, artistID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.ListResponse{
		Concerts: mapper.ConcertsToProto(concerts),
	}), nil
}

// ListByFollower returns all concerts for artists followed by the authenticated user,
// grouped by date and classified into geographic proximity lanes.
func (h *ConcertHandler) ListByFollower(ctx context.Context, _ *connect.Request[concertv1.ListByFollowerRequest]) (*connect.Response[concertv1.ListByFollowerResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	groups, err := h.concertUseCase.ListByFollowerGrouped(ctx, userID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.ListByFollowerResponse{
		Groups: mapper.ProximityGroupsToProto(groups),
	}), nil
}

// ListWithProximity returns concerts for the specified artists, grouped by date
// and classified by geographic proximity to the caller's home area.
// Authentication is not required.
func (h *ConcertHandler) ListWithProximity(ctx context.Context, req *connect.Request[concertv1.ListWithProximityRequest]) (*connect.Response[concertv1.ListWithProximityResponse], error) {
	ids := req.Msg.GetArtistIds()
	artistIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		artistIDs = append(artistIDs, id.GetValue())
	}

	home := mapper.ProtoHomeToEntity(req.Msg.GetHome())

	groups, err := h.concertUseCase.ListWithProximity(ctx, artistIDs, home)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.ListWithProximityResponse{
		Groups: mapper.ProximityGroupsToProto(groups),
	}), nil
}

// SearchNewConcerts enqueues an asynchronous concert discovery job and returns immediately.
// The actual search runs in a background goroutine. Use GetSearchStatus to poll for completion.
func (h *ConcertHandler) SearchNewConcerts(ctx context.Context, req *connect.Request[concertv1.SearchNewConcertsRequest]) (*connect.Response[concertv1.SearchNewConcertsResponse], error) {
	artistID := req.Msg.GetArtistId().GetValue()
	if artistID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("artist_id cannot be empty"))
	}

	if err := h.concertUseCase.AsyncSearchNewConcerts(ctx, artistID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.SearchNewConcertsResponse{}), nil
}

// ListSearchStatuses returns the current search status for one or more artists.
func (h *ConcertHandler) ListSearchStatuses(ctx context.Context, req *connect.Request[concertv1.ListSearchStatusesRequest]) (*connect.Response[concertv1.ListSearchStatusesResponse], error) {
	ids := req.Msg.GetArtistIds()
	if len(ids) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("artist_ids cannot be empty"))
	}

	artistIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		artistIDs = append(artistIDs, id.GetValue())
	}

	statuses, err := h.concertUseCase.ListSearchStatuses(ctx, artistIDs)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.ListSearchStatusesResponse{
		Statuses: mapper.SearchStatusesToProto(statuses),
	}), nil
}
