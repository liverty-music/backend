// Package rpc implements Connect-RPC handlers for the application's gRPC services.
package rpc

import (
	"context"

	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// ConcertHandler implements the ConcertService Connect interface.
type ConcertHandler struct {
	concertUseCase        usecase.ConcertUseCase
	userRepo              entity.UserRepository
	seriesImageURLBuilder *mapper.SeriesImageURLBuilder
	logger                *logging.Logger
}

// NewConcertHandler creates a new concert handler. imageURLBuilder composes
// the cover image URLs of the first-party Series every response returns.
func NewConcertHandler(
	concertUseCase usecase.ConcertUseCase,
	userRepo entity.UserRepository,
	seriesImageURLBuilder *mapper.SeriesImageURLBuilder,
	logger *logging.Logger,
) *ConcertHandler {
	return &ConcertHandler{
		concertUseCase:        concertUseCase,
		userRepo:              userRepo,
		seriesImageURLBuilder: seriesImageURLBuilder,
		logger:                logger,
	}
}

// Get returns the Concert of one Event whose Series has an event page.
// Authentication is not required.
func (h *ConcertHandler) Get(ctx context.Context, req *concertv1.GetRequest) (*concertv1.GetResponse, error) {
	concert, err := h.concertUseCase.Get(ctx, req.GetEventId().GetValue())
	if err != nil {
		return nil, err
	}

	return &concertv1.GetResponse{
		Concert: mapper.ConcertToProto(concert),
		Series:  h.seriesImageURLBuilder.ReferencedSeries([]*entity.Concert{concert}),
		Artists: mapper.ReferencedArtists([]*entity.Concert{concert}),
	}, nil
}

// ListBySeries returns the Concerts of one Series that has an event page, in
// date and start-time order. Authentication is not required.
func (h *ConcertHandler) ListBySeries(ctx context.Context, req *concertv1.ListBySeriesRequest) (*concertv1.ListBySeriesResponse, error) {
	concerts, err := h.concertUseCase.ListBySeries(ctx, req.GetSeriesId().GetValue())
	if err != nil {
		return nil, err
	}

	return &concertv1.ListBySeriesResponse{
		Concerts: mapper.ConcertsToProto(concerts),
		Series:   h.seriesImageURLBuilder.ReferencedSeries(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}, nil
}

// ListByArtist returns the Concerts of one Artist as a flat list, empty when
// the Artist has none. Authentication is not required.
func (h *ConcertHandler) ListByArtist(ctx context.Context, req *concertv1.ListByArtistRequest) (*concertv1.ListByArtistResponse, error) {
	concerts, err := h.concertUseCase.ListByArtist(ctx, req.GetArtistId().GetValue())
	if err != nil {
		return nil, err
	}

	return &concertv1.ListByArtistResponse{
		Concerts: mapper.ConcertsToProto(concerts),
		Series:   h.seriesImageURLBuilder.ReferencedSeries(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}, nil
}

// ListFollowed returns the concerts of the artists the signed-in fan follows,
// grouped by date and classified into proximity lanes around the fan's stored
// home. The request names no fan and no home.
func (h *ConcertHandler) ListFollowed(ctx context.Context, req *concertv1.ListFollowedRequest) (*concertv1.ListFollowedResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	// Optional lower bound. A nil from (field omitted) defaults to today-onward
	// via the repository's COALESCE(..., CURRENT_DATE); a client-supplied date
	// widens the window into the past.
	from := mapper.DateToTimePtr(req.GetFrom().GetValue())

	groups, err := h.concertUseCase.ListFollowed(ctx, user.ID, user.Home, from)
	if err != nil {
		return nil, err
	}

	concerts := mapper.ConcertsOfGroups(groups)
	return &concertv1.ListFollowedResponse{
		Groups:  mapper.ProximityGroupsToProto(groups),
		Series:  h.seriesImageURLBuilder.ReferencedSeries(concerts),
		Artists: mapper.ReferencedArtists(concerts),
	}, nil
}

// ListByArtists returns concerts for the specified artists, grouped by date
// and classified by geographic proximity to the caller's home area.
// Authentication is not required.
func (h *ConcertHandler) ListByArtists(ctx context.Context, req *concertv1.ListByArtistsRequest) (*concertv1.ListByArtistsResponse, error) {
	ids := req.GetArtistIds()
	artistIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		artistIDs = append(artistIDs, id.GetValue())
	}

	home := mapper.ProtoHomeToEntity(req.GetHome())

	groups, err := h.concertUseCase.ListByArtists(ctx, artistIDs, home)
	if err != nil {
		return nil, err
	}

	concerts := mapper.ConcertsOfGroups(groups)
	return &concertv1.ListByArtistsResponse{
		Groups:  mapper.ProximityGroupsToProto(groups),
		Series:  h.seriesImageURLBuilder.ReferencedSeries(concerts),
		Artists: mapper.ReferencedArtists(concerts),
	}, nil
}

// ListByLocation returns all concerts near the given reference point within a
// date range, grouped by date and classified by proximity (HOME/NEARBY only).
// Authentication is not required.
func (h *ConcertHandler) ListByLocation(ctx context.Context, req *concertv1.ListByLocationRequest) (*concertv1.ListByLocationResponse, error) {
	location := mapper.ProtoGeoLocationToEntity(req.GetLocation())
	from := mapper.DateToTime(req.GetFrom().GetValue())
	to := mapper.DateToTime(req.GetTo().GetValue())

	groups, err := h.concertUseCase.ListByLocation(ctx, location, from, to)
	if err != nil {
		return nil, err
	}

	concerts := mapper.ConcertsOfGroups(groups)
	return &concertv1.ListByLocationResponse{
		Groups:  mapper.ProximityGroupsToProto(groups),
		Series:  h.seriesImageURLBuilder.ReferencedSeries(concerts),
		Artists: mapper.ReferencedArtists(concerts),
	}, nil
}
