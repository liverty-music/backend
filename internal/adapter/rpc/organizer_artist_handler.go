package rpc

import (
	"context"

	organizerartistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/artist/v1/artistv1connect"
	organizerartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/artist/v1"
	"connectrpc.com/connect/v2"
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
// names an Organizer, so a member cannot ask for another Organizer's
// roster.
type OrganizerArtistHandler struct {
	artistUC    usecase.ArtistUseCase
	organizerUC usecase.OrganizerUseCase
	logger      *logging.Logger
}

// NewOrganizerArtistHandler creates a new OrganizerArtistHandler.
func NewOrganizerArtistHandler(artistUC usecase.ArtistUseCase, organizerUC usecase.OrganizerUseCase, logger *logging.Logger) *OrganizerArtistHandler {
	return &OrganizerArtistHandler{artistUC: artistUC, organizerUC: organizerUC, logger: logger}
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
	_ *organizerartistv1.ListRequest,
) (*organizerartistv1.ListResponse, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		// Defence-in-depth: the interceptor should have populated this; if it
		// did not, we must not proceed.
		return nil, connect.NewError(connect.CodePermissionDenied, "permission denied")
	}
	organizer, err := h.organizerUC.ResolveCaller(ctx, callerOrgID)
	if err != nil {
		return nil, err
	}

	// The request names no Organizer, so the caller's own Organizer is both
	// the caller and the requested one.
	artists, err := h.artistUC.ListByOrganizer(ctx, organizer.ID)
	if err != nil {
		return nil, err
	}

	return &organizerartistv1.ListResponse{
		Artists: mapper.ArtistsToProto(artists),
	}, nil
}
