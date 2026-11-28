package rpc

import (
	"context"

	adminofficialsitev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/official_site/v1/official_sitev1connect"
	adminofficialsitev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/official_site/v1"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that AdminOfficialSiteHandler satisfies the generated
// interface.
var _ adminofficialsitev1connect.OfficialSiteServiceHandler = (*AdminOfficialSiteHandler)(nil)

// AdminOfficialSiteHandler implements the admin OfficialSiteService Connect
// interface: an artist's official site is created and corrected only by an
// admin. Admin authorization is enforced at the admin server boundary by its
// server-wide RequireRoleInterceptor (admin role), and requests are validated
// by the server's protovalidate interceptor before this runs.
type AdminOfficialSiteHandler struct {
	officialSiteUC usecase.OfficialSiteUseCase
	logger         *logging.Logger
}

// NewAdminOfficialSiteHandler creates a new AdminOfficialSiteHandler.
func NewAdminOfficialSiteHandler(officialSiteUC usecase.OfficialSiteUseCase, logger *logging.Logger) *AdminOfficialSiteHandler {
	return &AdminOfficialSiteHandler{officialSiteUC: officialSiteUC, logger: logger}
}

// Create stores the official site URL of an artist that has none.
func (h *AdminOfficialSiteHandler) Create(ctx context.Context, req *adminofficialsitev1.CreateRequest) (*adminofficialsitev1.CreateResponse, error) {
	if err := h.officialSiteUC.Create(ctx, req.GetArtistId().GetValue(), req.GetUrl().GetValue()); err != nil {
		return nil, err
	}
	return &adminofficialsitev1.CreateResponse{}, nil
}

// Update replaces the URL of an artist's official site.
func (h *AdminOfficialSiteHandler) Update(ctx context.Context, req *adminofficialsitev1.UpdateRequest) (*adminofficialsitev1.UpdateResponse, error) {
	if err := h.officialSiteUC.Update(ctx, req.GetArtistId().GetValue(), req.GetUrl().GetValue()); err != nil {
		return nil, err
	}
	return &adminofficialsitev1.UpdateResponse{}, nil
}
