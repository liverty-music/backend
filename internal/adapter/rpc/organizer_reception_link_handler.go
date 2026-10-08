package rpc

import (
	"context"
	"errors"
	"time"

	receptionlinkv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/reception_link/v1/reception_linkv1connect"
	receptionlinkv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/reception_link/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"uuid"
)

// Compile-time assertion that OrganizerReceptionLinkHandler satisfies the
// generated interface.
var _ receptionlinkv1connect.ReceptionLinkServiceHandler = (*OrganizerReceptionLinkHandler)(nil)

// OrganizerReceptionLinkHandler implements the organizer-facing
// ReceptionLinkService. Org-scoped authorization is enforced by the
// OrgScopedInterceptor before this handler runs; the handler resolves the
// caller's own Organizer and delegates to the reception link use case.
type OrganizerReceptionLinkHandler struct {
	receptionLinkUC usecase.ReceptionLinkUseCase
	organizerUC     usecase.OrganizerUseCase
	logger          *logging.Logger
}

// NewOrganizerReceptionLinkHandler creates a new OrganizerReceptionLinkHandler.
func NewOrganizerReceptionLinkHandler(
	receptionLinkUC usecase.ReceptionLinkUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *OrganizerReceptionLinkHandler {
	return &OrganizerReceptionLinkHandler{
		receptionLinkUC: receptionLinkUC,
		organizerUC:     organizerUC,
		logger:          logger,
	}
}

// resolveCallerOrganizer reads the Zitadel org id from context and delegates
// to OrganizerUseCase.ResolveCaller, whose failure is returned unchanged.
func (h *OrganizerReceptionLinkHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	return h.organizerUC.ResolveCaller(ctx, callerOrgID)
}

// requireUUID fails with InvalidArgument when value is missing or not a UUID.
func requireUUID(field, value string) error {
	if value == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New(field+" is required"))
	}
	if _, err := uuid.Parse(value); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New(field+" must be a UUID"))
	}
	return nil
}

// Issue creates a ReceptionLink for one of the caller's published events.
func (h *OrganizerReceptionLinkHandler) Issue(
	ctx context.Context,
	req *connect.Request[receptionlinkv1.IssueRequest],
) (*connect.Response[receptionlinkv1.IssueResponse], error) {
	eventID := req.Msg.GetEventId().GetValue()
	if err := requireUUID("event_id", eventID); err != nil {
		return nil, err
	}
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	link, err := h.receptionLinkUC.Issue(ctx, organizer.ID, eventID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&receptionlinkv1.IssueResponse{
		ReceptionLink: mapper.ReceptionLinkToProto(link),
	}), nil
}

// List returns every ReceptionLink of one of the caller's events.
func (h *OrganizerReceptionLinkHandler) List(
	ctx context.Context,
	req *connect.Request[receptionlinkv1.ListRequest],
) (*connect.Response[receptionlinkv1.ListResponse], error) {
	eventID := req.Msg.GetEventId().GetValue()
	if err := requireUUID("event_id", eventID); err != nil {
		return nil, err
	}
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	links, err := h.receptionLinkUC.ListByEvent(ctx, organizer.ID, eventID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&receptionlinkv1.ListResponse{
		ReceptionLinks: mapper.ReceptionLinksToProto(links),
	}), nil
}

// Revoke stops one of the caller's ReceptionLinks at once.
func (h *OrganizerReceptionLinkHandler) Revoke(
	ctx context.Context,
	req *connect.Request[receptionlinkv1.RevokeRequest],
) (*connect.Response[receptionlinkv1.RevokeResponse], error) {
	linkID := req.Msg.GetReceptionLinkId().GetValue()
	if err := requireUUID("reception_link_id", linkID); err != nil {
		return nil, err
	}
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	link, err := h.receptionLinkUC.Revoke(ctx, organizer.ID, entity.ReceptionLinkID(linkID), time.Now())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&receptionlinkv1.RevokeResponse{
		ReceptionLink: mapper.ReceptionLinkToProto(link),
	}), nil
}
