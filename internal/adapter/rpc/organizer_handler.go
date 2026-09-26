package rpc

import (
	"context"
	"errors"

	organizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/v1/organizerv1connect"
	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that OrganizerHandler satisfies the generated interface.
var _ organizerv1connect.OrganizerServiceHandler = (*OrganizerHandler)(nil)

// OrganizerHandler implements the organizer-facing OrganizerService Connect
// interface. Org-scoped authorization (token audience check, login-scope org
// derivation, role cross-check) is enforced structurally by the
// OrgScopedInterceptor before this handler runs. The handler resolves the
// caller's Organizer from the context-stored Zitadel org id, checks its
// lifecycle status, and applies any request-body authorization (e.g., verifying
// that the supplied organizer_id matches the resolved one).
//
// Business logic is delegated to the usecase layer following the same
// handler→usecase convention used by AdminOrganizerHandler and UserHandler.
type OrganizerHandler struct {
	organizerUC usecase.OrganizerUseCase
	logger      *logging.Logger
}

// NewOrganizerHandler creates a new OrganizerHandler.
func NewOrganizerHandler(organizerUC usecase.OrganizerUseCase, logger *logging.Logger) *OrganizerHandler {
	return &OrganizerHandler{
		organizerUC: organizerUC,
		logger:      logger,
	}
}

// Get returns the caller's own Organizer identity (id, name). The caller's
// Organizer is resolved from the Zitadel org id stored in the context by the
// OrgScopedInterceptor. No client-supplied id is accepted.
//
// Possible errors (beyond those enforced by the interceptor):
//   - PERMISSION_DENIED: no Organizer is linked to the caller's Zitadel org id.
//   - FAILED_PRECONDITION: the resolved Organizer is deactivated.
func (h *OrganizerHandler) Get(
	ctx context.Context,
	_ *connect.Request[organizerv1.GetRequest],
) (*connect.Response[organizerv1.GetResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&organizerv1.GetResponse{
		Organizer: mapper.OrganizerToProto(organizer),
	}), nil
}

// ListArtists returns the artists the caller's own Organizer represents,
// ascending by artist id. The supplied organizer_id must match the resolved
// Organizer; the usecase rejects any other value with PermissionDenied.
func (h *OrganizerHandler) ListArtists(
	ctx context.Context,
	req *connect.Request[organizerv1.ListArtistsRequest],
) (*connect.Response[organizerv1.ListArtistsResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	// The protovalidate interceptor has already confirmed organizer_id is
	// non-empty and well-formed; the usecase enforces the ownership
	// invariant against the resolved caller Organizer.
	artists, err := h.organizerUC.ListOwnArtists(ctx, organizer.ID, req.Msg.GetOrganizerId().GetValue())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerv1.ListArtistsResponse{
		Artists: mapper.ArtistsToProto(artists),
	}), nil
}

// resolveCallerOrganizer reads the caller's Zitadel org id from the context
// (placed there by OrgScopedInterceptor) and delegates to the usecase to
// resolve the linked Organizer and enforce its lifecycle status. It returns
// the active Organizer or the usecase's error.
func (h *OrganizerHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		// Defence-in-depth: the interceptor should have populated this; if it
		// did not, we must not proceed.
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}

	return h.organizerUC.ResolveCaller(ctx, callerOrgID)
}
