package rpc

import (
	"context"

	adminartistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/artist/v1/artistv1connect"
	adminartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/artist/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that AdminArtistHandler satisfies the generated
// interface.
var _ adminartistv1connect.ArtistServiceHandler = (*AdminArtistHandler)(nil)

// AdminArtistHandler implements the admin ArtistService Connect interface: the
// artist reads only the admin console offers. Admin authorization is enforced
// at the admin server boundary by its server-wide RequireRoleInterceptor
// (admin role), and requests are validated by the server's protovalidate
// interceptor before this runs.
type AdminArtistHandler struct {
	organizerUseCase usecase.OrganizerUseCase
	logger           *logging.Logger
}

// NewAdminArtistHandler creates a new AdminArtistHandler.
func NewAdminArtistHandler(organizerUseCase usecase.OrganizerUseCase, logger *logging.Logger) *AdminArtistHandler {
	return &AdminArtistHandler{organizerUseCase: organizerUseCase, logger: logger}
}

// ListByOrganizer returns the Artists the named Organizer represents, whatever
// its status. Returns NotFound when no organizer with the given id exists.
func (h *AdminArtistHandler) ListByOrganizer(
	ctx context.Context,
	req *connect.Request[adminartistv1.ListByOrganizerRequest],
) (*connect.Response[adminartistv1.ListByOrganizerResponse], error) {
	artists, err := h.organizerUseCase.ListArtists(ctx, req.Msg.GetOrganizerId().GetValue())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminartistv1.ListByOrganizerResponse{Artists: mapper.ArtistsToProto(artists)}), nil
}
