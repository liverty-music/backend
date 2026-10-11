package rpc

import (
	"context"
	"errors"

	organizerartistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/artist/v1/artistv1connect"
	organizerartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/artist/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that OrganizerArtistHandler satisfies the generated
// interface.
var _ organizerartistv1connect.ArtistServiceHandler = (*OrganizerArtistHandler)(nil)

// OrganizerArtistHandler implements the organizer-facing ArtistService Connect
// interface. The organizer-console sign-in checks run in the
// OrgScopedInterceptor before this handler; the handler then resolves the
// caller's own Organizer from the context-stored Zitadel org id. No request
// names an Organizer, so an operator cannot ask for another Organizer's
// roster.
type OrganizerArtistHandler struct {
	organizerUC usecase.OrganizerUseCase
	logger      *logging.Logger
}

// NewOrganizerArtistHandler creates a new OrganizerArtistHandler.
func NewOrganizerArtistHandler(organizerUC usecase.OrganizerUseCase, logger *logging.Logger) *OrganizerArtistHandler {
	return &OrganizerArtistHandler{organizerUC: organizerUC, logger: logger}
}

// List returns every Artist the caller's own Organizer represents, empty when
// it represents none.
//
// Possible errors (beyond those enforced by the interceptor):
//   - PERMISSION_DENIED: no Organizer is linked to the caller's Zitadel org
//     id, or it is provisioning.
//   - FAILED_PRECONDITION: the resolved Organizer is deactivated.
func (h *OrganizerArtistHandler) List(
	ctx context.Context,
	_ *connect.Request[organizerartistv1.ListRequest],
) (*connect.Response[organizerartistv1.ListResponse], error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		// Defence-in-depth: the interceptor should have populated this; if it
		// did not, we must not proceed.
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	organizer, err := h.organizerUC.ResolveCaller(ctx, callerOrgID)
	if err != nil {
		return nil, err
	}

	// The request names no Organizer, so the caller's own Organizer is both
	// the caller and the requested one.
	artists, err := h.organizerUC.ListOwnArtists(ctx, organizer.ID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerartistv1.ListResponse{
		Artists: mapper.ArtistsToProto(artists),
	}), nil
}
