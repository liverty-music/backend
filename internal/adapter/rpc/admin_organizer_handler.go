package rpc

import (
	"context"

	adminorganizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/organizer/v1/organizerv1connect"
	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/organizer/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that AdminOrganizerHandler satisfies the generated
// interface.
var _ adminorganizerv1connect.OrganizerServiceHandler = (*AdminOrganizerHandler)(nil)

// AdminOrganizerHandler implements the admin OrganizerService Connect interface.
// It does not list an Organizer's Artists; the admin ArtistService does
// ([AdminArtistHandler.ListByOrganizer]).
// Admin authorization is enforced at the admin server boundary by its server-wide
// RequireRoleInterceptor (admin role), not by per-method checks here.
type AdminOrganizerHandler struct {
	organizerUseCase usecase.OrganizerUseCase
	logger           *logging.Logger
}

// NewAdminOrganizerHandler creates a new AdminOrganizerHandler.
func NewAdminOrganizerHandler(organizerUseCase usecase.OrganizerUseCase, logger *logging.Logger) *AdminOrganizerHandler {
	return &AdminOrganizerHandler{organizerUseCase: organizerUseCase, logger: logger}
}

// Create registers a new Organizer with its short name and owner email,
// provisions its Organization at the identity provider, then returns the
// persisted record.
func (h *AdminOrganizerHandler) Create(
	ctx context.Context,
	req *organizerv1.CreateRequest,
) (*organizerv1.CreateResponse, error) {
	organizer, err := h.organizerUseCase.Create(ctx,
		req.GetName().GetValue(), req.GetShortName().GetValue(), req.GetOwnerEmail().GetValue())
	if err != nil {
		return nil, err
	}
	return &organizerv1.CreateResponse{Organizer: pb.FromOrganizer(organizer)}, nil
}

// AssociateArtist links an existing artist to an Organizer. Returns
// AlreadyExists if the artist is already represented by an organizer and
// NotFound if either the organizer or the artist does not exist.
func (h *AdminOrganizerHandler) AssociateArtist(
	ctx context.Context,
	req *organizerv1.AssociateArtistRequest,
) (*organizerv1.AssociateArtistResponse, error) {
	if err := h.organizerUseCase.AssociateArtist(ctx, req.GetOrganizerId().GetValue(), req.GetArtistId().GetValue()); err != nil {
		return nil, err
	}
	return &organizerv1.AssociateArtistResponse{}, nil
}

// DisassociateArtist removes the link between an Organizer and an artist
// (idempotent). Returns NotFound if the organizer does not exist.
func (h *AdminOrganizerHandler) DisassociateArtist(
	ctx context.Context,
	req *organizerv1.DisassociateArtistRequest,
) (*organizerv1.DisassociateArtistResponse, error) {
	if err := h.organizerUseCase.DisassociateArtist(ctx, req.GetOrganizerId().GetValue(), req.GetArtistId().GetValue()); err != nil {
		return nil, err
	}
	return &organizerv1.DisassociateArtistResponse{}, nil
}

// List returns every Organizer regardless of status.
func (h *AdminOrganizerHandler) List(
	ctx context.Context,
	_ *organizerv1.ListRequest,
) (*organizerv1.ListResponse, error) {
	organizers, err := h.organizerUseCase.List(ctx)
	if err != nil {
		return nil, err
	}
	return &organizerv1.ListResponse{Organizers: pb.FromOrganizers(organizers)}, nil
}

// Get returns a single Organizer by id. Returns NotFound when no organizer
// with the given id exists.
func (h *AdminOrganizerHandler) Get(
	ctx context.Context,
	req *organizerv1.GetRequest,
) (*organizerv1.GetResponse, error) {
	organizer, err := h.organizerUseCase.Get(ctx, req.GetOrganizerId().GetValue())
	if err != nil {
		return nil, err
	}
	return &organizerv1.GetResponse{Organizer: pb.FromOrganizer(organizer)}, nil
}

// Deactivate turns an Organizer off: deactivates its members' identities,
// releases artist associations, and marks the Organizer deactivated
// (idempotent). Returns NotFound when no organizer with the given id exists.
func (h *AdminOrganizerHandler) Deactivate(
	ctx context.Context,
	req *organizerv1.DeactivateRequest,
) (*organizerv1.DeactivateResponse, error) {
	if err := h.organizerUseCase.Deactivate(ctx, req.GetOrganizerId().GetValue()); err != nil {
		return nil, err
	}
	return &organizerv1.DeactivateResponse{}, nil
}

// Delete permanently removes a deactivated Organizer with its files,
// Organization and records. Returns NotFound when no organizer with the given id exists and
// FailedPrecondition when it is not deactivated or deletion is blocked.
func (h *AdminOrganizerHandler) Delete(
	ctx context.Context,
	req *organizerv1.DeleteRequest,
) (*organizerv1.DeleteResponse, error) {
	if err := h.organizerUseCase.Delete(ctx, req.GetOrganizerId().GetValue()); err != nil {
		return nil, err
	}
	return &organizerv1.DeleteResponse{}, nil
}

// SetBusinessDetails records an Organizer's five business details (the 特商法
// disclosure) and returns the Organizer with them. The request is validated by
// the server's protovalidate interceptor before this runs.
func (h *AdminOrganizerHandler) SetBusinessDetails(
	ctx context.Context,
	req *organizerv1.SetBusinessDetailsRequest,
) (*organizerv1.SetBusinessDetailsResponse, error) {
	m := req
	organizer, err := h.organizerUseCase.SetBusinessDetails(ctx, m.GetOrganizerId().GetValue(),
		pb.BusinessDetailsFromSetRequest(m.GetLegalName(), m.GetRepresentativeName(), m.GetBusinessAddress(), m.GetBusinessPhoneNumber(), m.GetContactEmail()))
	if err != nil {
		return nil, err
	}
	return &organizerv1.SetBusinessDetailsResponse{Organizer: pb.FromOrganizer(organizer)}, nil
}

// SetPlatformFeeRate sets the platform fee rate of an Organizer's future
// Orders and returns the Organizer with it.
func (h *AdminOrganizerHandler) SetPlatformFeeRate(
	ctx context.Context,
	req *organizerv1.SetPlatformFeeRateRequest,
) (*organizerv1.SetPlatformFeeRateResponse, error) {
	organizer, err := h.organizerUseCase.SetPlatformFeeRate(ctx,
		req.GetOrganizerId().GetValue(), int(req.GetPlatformFeeRateBps()))
	if err != nil {
		return nil, err
	}
	return &organizerv1.SetPlatformFeeRateResponse{Organizer: pb.FromOrganizer(organizer)}, nil
}
