package rpc

import (
	"context"

	adminareav1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/area/v1/areav1connect"
	adminareav1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/area/v1"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that AdminAreaHandler satisfies the generated interface.
var _ adminareav1connect.AreaServiceHandler = (*AdminAreaHandler)(nil)

// AdminAreaHandler implements the admin AreaService: an admin approving a
// discovered concert resolves the area they entered and lists a country's time
// zones. Admin authorization is the admin server's RequireRoleInterceptor; the
// handler is proto<->entity mapping.
type AdminAreaHandler struct {
	areaUC usecase.AreaUseCase
}

// NewAdminAreaHandler creates a new AdminAreaHandler.
func NewAdminAreaHandler(areaUC usecase.AreaUseCase) *AdminAreaHandler {
	return &AdminAreaHandler{areaUC: areaUC}
}

// Resolve returns the first-level Area an entered area text stands for.
func (h *AdminAreaHandler) Resolve(ctx context.Context, req *adminareav1.ResolveRequest) (*adminareav1.ResolveResponse, error) {
	var country *string
	if req.HasCountryCode() {
		c := req.GetCountryCode().GetValue()
		country = &c
	}
	area, err := h.areaUC.Resolve(ctx, req.GetArea().GetValue(), country)
	if err != nil {
		return nil, err
	}
	return &adminareav1.ResolveResponse{Area: pb.FromArea(area)}, nil
}

// ListByCountry returns a country's first-level Areas, regions and time zones.
func (h *AdminAreaHandler) ListByCountry(
	ctx context.Context,
	req *adminareav1.ListByCountryRequest,
) (*adminareav1.ListByCountryResponse, error) {
	country, err := h.areaUC.ListByCountry(ctx, req.GetCountryCode().GetValue())
	if err != nil {
		return nil, err
	}
	return &adminareav1.ListByCountryResponse{
		Areas:     pb.FromAreas(country.Areas),
		Regions:   pb.FromAreas(country.Regions),
		TimeZones: pb.FromTimeZoneOptions(country.TimeZones),
	}, nil
}
