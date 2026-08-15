package rpc

import (
	"context"

	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/organizer/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// AdminOrganizerHandler implements the admin OrganizerService Connect interface.
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

// Create registers a new Organizer and provisions its Zitadel tenant, then
// returns the persisted record.
func (h *AdminOrganizerHandler) Create(
	ctx context.Context,
	req *connect.Request[organizerv1.CreateRequest],
) (*connect.Response[organizerv1.CreateResponse], error) {
	organizer, err := h.organizerUseCase.Create(ctx, req.Msg.GetName().GetValue(), req.Msg.GetOperatorEmail().GetValue())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&organizerv1.CreateResponse{Organizer: mapper.OrganizerToProto(organizer)}), nil
}

// AssociateArtist links an existing artist to an Organizer. Returns
// AlreadyExists if the artist is already represented by an organizer and
// NotFound if either the organizer or the artist does not exist.
func (h *AdminOrganizerHandler) AssociateArtist(
	ctx context.Context,
	req *connect.Request[organizerv1.AssociateArtistRequest],
) (*connect.Response[organizerv1.AssociateArtistResponse], error) {
	if err := h.organizerUseCase.AssociateArtist(ctx, req.Msg.GetOrganizerId().GetValue(), req.Msg.GetArtistId().GetValue()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&organizerv1.AssociateArtistResponse{}), nil
}

// DisassociateArtist removes the link between an Organizer and an artist
// (idempotent). Returns NotFound if the organizer does not exist.
func (h *AdminOrganizerHandler) DisassociateArtist(
	ctx context.Context,
	req *connect.Request[organizerv1.DisassociateArtistRequest],
) (*connect.Response[organizerv1.DisassociateArtistResponse], error) {
	if err := h.organizerUseCase.DisassociateArtist(ctx, req.Msg.GetOrganizerId().GetValue(), req.Msg.GetArtistId().GetValue()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&organizerv1.DisassociateArtistResponse{}), nil
}

// List returns every Organizer regardless of status.
func (h *AdminOrganizerHandler) List(
	ctx context.Context,
	_ *connect.Request[organizerv1.ListRequest],
) (*connect.Response[organizerv1.ListResponse], error) {
	organizers, err := h.organizerUseCase.List(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&organizerv1.ListResponse{Organizers: mapper.OrganizersToProto(organizers)}), nil
}

// Get returns a single Organizer by id. Returns NotFound when no organizer
// with the given id exists.
func (h *AdminOrganizerHandler) Get(
	ctx context.Context,
	req *connect.Request[organizerv1.GetRequest],
) (*connect.Response[organizerv1.GetResponse], error) {
	organizer, err := h.organizerUseCase.Get(ctx, req.Msg.GetOrganizerId().GetValue())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&organizerv1.GetResponse{Organizer: mapper.OrganizerToProto(organizer)}), nil
}

// ListArtists returns the artists represented by an Organizer. Returns
// NotFound when no organizer with the given id exists.
func (h *AdminOrganizerHandler) ListArtists(
	ctx context.Context,
	req *connect.Request[organizerv1.ListArtistsRequest],
) (*connect.Response[organizerv1.ListArtistsResponse], error) {
	artists, err := h.organizerUseCase.ListArtists(ctx, req.Msg.GetOrganizerId().GetValue())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&organizerv1.ListArtistsResponse{Artists: mapper.ArtistsToProto(artists)}), nil
}

// Deactivate turns an Organizer off: deactivates its Zitadel operators,
// releases artist associations, and marks the Organizer deactivated
// (idempotent). Returns NotFound when no organizer with the given id exists.
func (h *AdminOrganizerHandler) Deactivate(
	ctx context.Context,
	req *connect.Request[organizerv1.DeactivateRequest],
) (*connect.Response[organizerv1.DeactivateResponse], error) {
	if err := h.organizerUseCase.Deactivate(ctx, req.Msg.GetOrganizerId().GetValue()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&organizerv1.DeactivateResponse{}), nil
}
