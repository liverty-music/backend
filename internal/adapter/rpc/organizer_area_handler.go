package rpc

import (
	"context"

	organizerareav1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/area/v1/areav1connect"
	organizerareav1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/area/v1"
	"connectrpc.com/connect/v2"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/authctx"
)

// Compile-time assertion that OrganizerAreaHandler satisfies the generated interface.
var _ organizerareav1connect.AreaServiceHandler = (*OrganizerAreaHandler)(nil)

// OrganizerAreaHandler implements the organizer-facing AreaService: the
// countries a venue can be in, and a country's first-level Areas, regions and
// time zones, for a member placing an event. Each call first resolves the
// caller's own active Organizer.
type OrganizerAreaHandler struct {
	areaUC      usecase.AreaUseCase
	organizerUC usecase.OrganizerUseCase
}

// NewOrganizerAreaHandler creates a new OrganizerAreaHandler.
func NewOrganizerAreaHandler(areaUC usecase.AreaUseCase, organizerUC usecase.OrganizerUseCase) *OrganizerAreaHandler {
	return &OrganizerAreaHandler{areaUC: areaUC, organizerUC: organizerUC}
}

// resolveCaller fails unless the caller acts for an active Organizer.
func (h *OrganizerAreaHandler) resolveCaller(ctx context.Context) error {
	callerOrgID, ok := authctx.GetCallerOrgID(ctx)
	if !ok {
		return connect.NewError(connect.CodePermissionDenied, "permission denied")
	}
	_, err := h.organizerUC.ResolveCaller(ctx, callerOrgID)
	return err
}

// ListCountries returns the countries and territories a venue can be in.
func (h *OrganizerAreaHandler) ListCountries(
	ctx context.Context,
	_ *organizerareav1.ListCountriesRequest,
) (*organizerareav1.ListCountriesResponse, error) {
	if err := h.resolveCaller(ctx); err != nil {
		return nil, err
	}
	countries, err := h.areaUC.ListCountries(ctx)
	if err != nil {
		return nil, err
	}
	return &organizerareav1.ListCountriesResponse{Countries: pb.FromCountries(countries)}, nil
}

// ListByCountry returns a country's first-level Areas, regions and time zones.
func (h *OrganizerAreaHandler) ListByCountry(
	ctx context.Context,
	req *organizerareav1.ListByCountryRequest,
) (*organizerareav1.ListByCountryResponse, error) {
	if err := h.resolveCaller(ctx); err != nil {
		return nil, err
	}
	country, err := h.areaUC.ListByCountry(ctx, req.GetCountryCode().GetValue())
	if err != nil {
		return nil, err
	}
	return &organizerareav1.ListByCountryResponse{
		Areas:     pb.FromAreas(country.Areas),
		Regions:   pb.FromAreas(country.Regions),
		TimeZones: pb.FromTimeZoneOptions(country.TimeZones),
	}, nil
}
