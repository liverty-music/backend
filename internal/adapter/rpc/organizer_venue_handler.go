package rpc

import (
	"context"
	"errors"

	organizervenuev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/venue/v1/venuev1connect"
	organizervenuev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/venue/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that OrganizerVenueHandler satisfies the generated interface.
var _ organizervenuev1connect.VenueServiceHandler = (*OrganizerVenueHandler)(nil)

// OrganizerVenueHandler implements the organizer-facing VenueService Connect
// interface. It resolves the caller's organizer and delegates to the use case.
type OrganizerVenueHandler struct {
	authoringUC usecase.ConcertAuthoringUseCase
	organizerUC usecase.OrganizerUseCase
}

// NewOrganizerVenueHandler creates a new OrganizerVenueHandler.
func NewOrganizerVenueHandler(
	authoringUC usecase.ConcertAuthoringUseCase,
	organizerUC usecase.OrganizerUseCase,
) *OrganizerVenueHandler {
	return &OrganizerVenueHandler{authoringUC: authoringUC, organizerUC: organizerUC}
}

// resolveCallerOrganizer reads the identity provider's organization id from
// context and returns the caller's active Organizer.
func (h *OrganizerVenueHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	return h.organizerUC.ResolveCaller(ctx, callerOrgID)
}

// Search looks up a venue name in the map catalog for the caller's own
// active Organizer and returns the candidate places.
func (h *OrganizerVenueHandler) Search(
	ctx context.Context,
	req *connect.Request[organizervenuev1.SearchRequest],
) (*connect.Response[organizervenuev1.SearchResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	candidates, err := h.authoringUC.SearchPlaces(ctx, organizer.ID, req.Msg.GetText())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizervenuev1.SearchResponse{
		Candidates: mapper.VenueCandidatesToProto(candidates),
	}), nil
}
