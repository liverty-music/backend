package rpc

import (
	"context"

	backendorganizationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/organization/v1/organizationv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	backendorganizationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/organization/v1"

	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that BackendOrganizationHandler satisfies the generated interface.
var _ backendorganizationv1connect.OrganizationServiceHandler = (*BackendOrganizationHandler)(nil)

// BackendOrganizationHandler implements the identity service's
// OrganizationService, which the admin console API calls to create, provision
// and remove Organizers' Organizations. Like BackendUserHandler, each method
// calls the IdentityOrganizationUseCase and maps its result.
type BackendOrganizationHandler struct {
	organizationUC usecase.IdentityOrganizationUseCase
}

// NewBackendOrganizationHandler creates a new BackendOrganizationHandler.
func NewBackendOrganizationHandler(organizationUC usecase.IdentityOrganizationUseCase) *BackendOrganizationHandler {
	return &BackendOrganizationHandler{organizationUC: organizationUC}
}

// Create creates the Organization named by the short name and returns its id.
// Returns AlreadyExists when an Organization has that name.
func (h *BackendOrganizationHandler) Create(ctx context.Context, req *backendorganizationv1.CreateRequest) (*backendorganizationv1.CreateResponse, error) {
	id, err := h.organizationUC.Create(ctx, req.GetShortName().GetValue())
	if err != nil {
		return nil, err
	}
	return &backendorganizationv1.CreateResponse{ProviderOrganizerId: &entityv1.Organizer_ProviderOrganizerId{Value: id}}, nil
}

// Get returns the id of the Organization named by the short name. Returns
// NotFound when none has it.
func (h *BackendOrganizationHandler) Get(ctx context.Context, req *backendorganizationv1.GetRequest) (*backendorganizationv1.GetResponse, error) {
	id, err := h.organizationUC.GetByName(ctx, req.GetShortName().GetValue())
	if err != nil {
		return nil, err
	}
	return &backendorganizationv1.GetResponse{ProviderOrganizerId: &entityv1.Organizer_ProviderOrganizerId{Value: id}}, nil
}

// Provision completes the Organization and its owner identity.
func (h *BackendOrganizationHandler) Provision(ctx context.Context, req *backendorganizationv1.ProvisionRequest) (*backendorganizationv1.ProvisionResponse, error) {
	if err := h.organizationUC.Provision(ctx, req.GetProviderOrganizerId().GetValue(), req.GetShortName().GetValue(), req.GetOwnerEmail().GetValue()); err != nil {
		return nil, err
	}
	return &backendorganizationv1.ProvisionResponse{}, nil
}

// DeactivateIdentities makes every identity in the Organization unable to
// sign in.
func (h *BackendOrganizationHandler) DeactivateIdentities(ctx context.Context, req *backendorganizationv1.DeactivateIdentitiesRequest) (*backendorganizationv1.DeactivateIdentitiesResponse, error) {
	if err := h.organizationUC.DeactivateIdentities(ctx, req.GetProviderOrganizerId().GetValue()); err != nil {
		return nil, err
	}
	return &backendorganizationv1.DeactivateIdentitiesResponse{}, nil
}

// Delete removes the Organization with every identity in it.
func (h *BackendOrganizationHandler) Delete(ctx context.Context, req *backendorganizationv1.DeleteRequest) (*backendorganizationv1.DeleteResponse, error) {
	if err := h.organizationUC.Delete(ctx, req.GetProviderOrganizerId().GetValue()); err != nil {
		return nil, err
	}
	return &backendorganizationv1.DeleteResponse{}, nil
}
