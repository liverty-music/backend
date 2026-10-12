package rpc

import (
	"context"
	"errors"

	officialsitev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/official_site/v1/official_sitev1connect"
	officialsitev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/official_site/v1"
	"connectrpc.com/connect"
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
func (h *OfficialSiteHandler) Create(ctx context.Context, req *connect.Request[officialsitev1.CreateRequest]) (*connect.Response[officialsitev1.CreateResponse], error) {
	if err := h.officialSiteUC.Create(ctx, req.Msg.GetArtistId().GetValue(), req.Msg.GetUrl().GetValue()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&officialsitev1.CreateResponse{}), nil
}

// Delete always fails with Unimplemented: there is no usecase behind it, and
// nothing changes.
func (h *OfficialSiteHandler) Delete(_ context.Context, _ *connect.Request[officialsitev1.DeleteRequest]) (*connect.Response[officialsitev1.DeleteResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("deleting an official site is not supported"))
}
