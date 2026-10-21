package rpc

import (
	"context"

	officialsitev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/official_site/v1/official_sitev1connect"
	officialsitev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/official_site/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that OfficialSiteHandler satisfies the generated
// interface.
var _ officialsitev1connect.OfficialSiteServiceHandler = (*OfficialSiteHandler)(nil)

// OfficialSiteHandler implements the fan-facing OfficialSiteService Connect
// interface: an artist's official site is created through the boundary but
// never deleted. Both calls need a signed-in caller, which the server's authn
// middleware enforces; neither acts for a particular fan.
type OfficialSiteHandler struct {
	officialSiteUC usecase.OfficialSiteUseCase
	logger         *logging.Logger
}

// NewOfficialSiteHandler creates a new OfficialSiteHandler.
func NewOfficialSiteHandler(officialSiteUC usecase.OfficialSiteUseCase, logger *logging.Logger) *OfficialSiteHandler {
	return &OfficialSiteHandler{officialSiteUC: officialSiteUC, logger: logger}
}

// Create stores the official site URL of an artist.
func (h *OfficialSiteHandler) Create(ctx context.Context, req *officialsitev1.CreateRequest) (*officialsitev1.CreateResponse, error) {
	if err := h.officialSiteUC.Create(ctx, req.GetArtistId().GetValue(), req.GetUrl().GetValue()); err != nil {
		return nil, err
	}
	return &officialsitev1.CreateResponse{}, nil
}

// Delete always fails with Unimplemented: there is no usecase behind it, and
// nothing changes.
func (h *OfficialSiteHandler) Delete(_ context.Context, _ *officialsitev1.DeleteRequest) (*officialsitev1.DeleteResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, "deleting an official site is not supported")
}
