// Package rpc implements Connect-RPC handlers for the application's gRPC services.
package rpc

import (
	"context"

	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// ConcertHandler implements the ConcertService Connect interface.
type ConcertHandler struct {
	concertUseCase  usecase.ConcertUseCase
	userRepo        entity.UserRepository
	mediaURLBuilder *mapper.MediaURLBuilder
	logger          *logging.Logger
}

// NewConcertHandler creates a new concert handler. mediaURLBuilder composes
// the cover image URLs of the first-party Series returned by Get and
// ListBySeries.
func NewConcertHandler(
	concertUseCase usecase.ConcertUseCase,
	userRepo entity.UserRepository,
	mediaURLBuilder *mapper.MediaURLBuilder,
	logger *logging.Logger,
) *ConcertHandler {
	return &ConcertHandler{
		concertUseCase:  concertUseCase,
		userRepo:        userRepo,
		mediaURLBuilder: mediaURLBuilder,
		logger:          logger,
	}
}

// Get returns the Concert of one Event whose Series has an event page.
// Authentication is not required.
func (h *ConcertHandler) Get(ctx context.Context, req *connect.Request[concertv1.GetRequest]) (*connect.Response[concertv1.GetResponse], error) {
	concert, err := h.concertUseCase.Get(ctx, req.Msg.GetEventId().GetValue())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.GetResponse{
		Concert: h.mediaURLBuilder.ConcertToProto(concert),
	}), nil
}

// ListBySeries returns the Concerts of one Series that has an event page, in
// date and start-time order. Authentication is not required.
func (h *ConcertHandler) ListBySeries(ctx context.Context, req *connect.Request[concertv1.ListBySeriesRequest]) (*connect.Response[concertv1.ListBySeriesResponse], error) {
	concerts, err := h.concertUseCase.ListBySeries(ctx, req.Msg.GetSeriesId().GetValue())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.ListBySeriesResponse{
		Concerts: h.mediaURLBuilder.ConcertsToProto(concerts),
	}), nil
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
func (h *ConcertHandler) ListByFollower(ctx context.Context, req *connect.Request[concertv1.ListByFollowerRequest]) (*connect.Response[concertv1.ListByFollowerResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	// Optional lower bound. A nil from (field omitted) defaults to today-onward
	// via the repository's COALESCE(..., CURRENT_DATE); a client-supplied date
	// widens the window into the past.
	from := mapper.DateToTimePtr(req.Msg.GetFrom().GetValue())

	groups, err := h.concertUseCase.ListByFollowerGrouped(ctx, user.ID, user.Home, from)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.ListByFollowerResponse{
		Groups: mapper.ProximityGroupsToProto(groups),
	}), nil
}

// ListByArtists returns concerts for the specified artists, grouped by date
// and classified by geographic proximity to the caller's home area.
// Authentication is not required.
func (h *ConcertHandler) ListByArtists(ctx context.Context, req *connect.Request[concertv1.ListByArtistsRequest]) (*connect.Response[concertv1.ListByArtistsResponse], error) {
	ids := req.Msg.GetArtistIds()
	artistIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		artistIDs = append(artistIDs, id.GetValue())
	}

	home := mapper.ProtoHomeToEntity(req.Msg.GetHome())

	groups, err := h.concertUseCase.ListByArtists(ctx, artistIDs, home)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.ListByArtistsResponse{
		Groups: mapper.ProximityGroupsToProto(groups),
	}), nil
}

// ListByLocation returns all concerts near the given reference point within a
// date range, grouped by date and classified by proximity (HOME/NEARBY only).
// Authentication is not required.
func (h *ConcertHandler) ListByLocation(ctx context.Context, req *connect.Request[concertv1.ListByLocationRequest]) (*connect.Response[concertv1.ListByLocationResponse], error) {
	location := mapper.ProtoGeoLocationToEntity(req.Msg.GetLocation())
	from := mapper.DateToTime(req.Msg.GetFrom().GetValue())
	to := mapper.DateToTime(req.Msg.GetTo().GetValue())

	groups, err := h.concertUseCase.ListByLocation(ctx, location, from, to)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.ListByLocationResponse{
		Groups: mapper.ProximityGroupsToProto(groups),
	}), nil
}

// SearchNewConcerts discovers new concerts for the given artist synchronously
// and returns them in the response.
func (h *ConcertHandler) SearchNewConcerts(ctx context.Context, req *connect.Request[concertv1.SearchNewConcertsRequest]) (*connect.Response[concertv1.SearchNewConcertsResponse], error) {
	concerts, err := h.concertUseCase.SearchNewConcerts(ctx, req.Msg.GetArtistId().GetValue())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertv1.SearchNewConcertsResponse{
		Concerts: mapper.ConcertsToProto(concerts),
	}), nil
}
