package rpc

import (
	"context"

	adminartistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/artist/v1/artistv1connect"
	adminartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/artist/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
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
	artistUC usecase.ArtistUseCase
	logger   *logging.Logger
}

// NewAdminArtistHandler creates a new AdminArtistHandler.
func NewAdminArtistHandler(artistUC usecase.ArtistUseCase, logger *logging.Logger) *AdminArtistHandler {
	return &AdminArtistHandler{artistUC: artistUC, logger: logger}
}

// Get returns one Artist by its id. Returns NotFound when no artist has it.
func (h *AdminArtistHandler) Get(
	ctx context.Context,
	req *adminartistv1.GetRequest,
) (*adminartistv1.GetResponse, error) {
	artist, err := h.artistUC.Get(ctx, req.GetArtistId().GetValue())
	if err != nil {
		return nil, err
	}
	return &adminartistv1.GetResponse{Artist: pb.FromArtist(artist)}, nil
}

// ListByOrganizer returns the Artists the named Organizer represents, whatever
// its status. Returns NotFound when no organizer with the given id exists.
func (h *AdminArtistHandler) ListByOrganizer(
	ctx context.Context,
	req *adminartistv1.ListByOrganizerRequest,
) (*adminartistv1.ListByOrganizerResponse, error) {
	artists, err := h.artistUC.ListByOrganizer(ctx, req.GetOrganizerId().GetValue())
	if err != nil {
		return nil, err
	}
	return &adminartistv1.ListByOrganizerResponse{Artists: pb.FromArtists(artists)}, nil
}
