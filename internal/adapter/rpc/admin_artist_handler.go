package rpc

import (
	"context"

	adminartistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/artist/v1/artistv1connect"
	adminartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/artist/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that AdminArtistHandler satisfies the generated
// interface.
var _ adminartistv1connect.ArtistServiceHandler = (*AdminArtistHandler)(nil)

// AdminArtistHandler implements the admin ArtistService Connect interface: the
// artist calls only the admin console offers. Admin authorization is enforced
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

// Create registers an artist under the name and MBID an admin gives (the
// console's test-artist form) and returns the registered Artist. Usecase
// errors are returned unchanged.
func (h *AdminArtistHandler) Create(
	ctx context.Context,
	req *adminartistv1.CreateRequest,
) (*adminartistv1.CreateResponse, error) {
	created, err := h.artistUC.Create(ctx, entity.NewArtist(req.GetName().GetValue(), req.GetMbid().GetValue()))
	if err != nil {
		return nil, err
	}
	return &adminartistv1.CreateResponse{Artist: pb.FromArtist(created)}, nil
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

// ListByMbidStatus returns the Artists whose MBID is in the requested status,
// the one registered first listed first. An unspecified status is rejected
// with InvalidArgument before any usecase runs.
func (h *AdminArtistHandler) ListByMbidStatus(
	ctx context.Context,
	req *adminartistv1.ListByMbidStatusRequest,
) (*adminartistv1.ListByMbidStatusResponse, error) {
	status, ok := pb.ToMBIDStatus(req.GetMbidStatus())
	if !ok {
		return nil, apperr.New(codes.InvalidArgument, "mbid_status must be specified")
	}
	artists, err := h.artistUC.ListByMBIDStatus(ctx, status)
	if err != nil {
		return nil, err
	}
	return &adminartistv1.ListByMbidStatusResponse{Artists: pb.FromArtists(artists)}, nil
}

// CorrectMbid gives the Artist the MBID an admin chose and returns the
// corrected Artist. Usecase errors (NotFound, AlreadyExists, Unavailable) are
// returned unchanged.
func (h *AdminArtistHandler) CorrectMbid(
	ctx context.Context,
	req *adminartistv1.CorrectMbidRequest,
) (*adminartistv1.CorrectMbidResponse, error) {
	artist, err := h.artistUC.CorrectMBID(ctx, req.GetArtistId().GetValue(), req.GetMbid().GetValue())
	if err != nil {
		return nil, err
	}
	return &adminartistv1.CorrectMbidResponse{Artist: pb.FromArtist(artist)}, nil
}
