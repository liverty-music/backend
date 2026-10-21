package rpc

import (
	"context"

	tickettypev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/ticket_type/v1/ticket_typev1connect"
	tickettypev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_type/v1"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that OrganizerTicketTypeHandler satisfies the
// generated interface.
var _ tickettypev1connect.TicketTypeServiceHandler = (*OrganizerTicketTypeHandler)(nil)

// OrganizerTicketTypeHandler implements the organizer-facing
// TicketTypeService: a member changes what one of their sales offers for an
// event. It resolves the caller's own Organizer and delegates to the use case.
type OrganizerTicketTypeHandler struct {
	ticketTypeUC usecase.TicketTypeUseCase
	organizerUC  usecase.OrganizerUseCase
	logger       *logging.Logger
}

// NewOrganizerTicketTypeHandler creates a new OrganizerTicketTypeHandler.
func NewOrganizerTicketTypeHandler(
	ticketTypeUC usecase.TicketTypeUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *OrganizerTicketTypeHandler {
	return &OrganizerTicketTypeHandler{ticketTypeUC: ticketTypeUC, organizerUC: organizerUC, logger: logger}
}

// Update changes the price, quantity and per-account limit of one of the
// caller's TicketTypes.
func (h *OrganizerTicketTypeHandler) Update(
	ctx context.Context,
	req *tickettypev1.UpdateRequest,
) (*tickettypev1.UpdateResponse, error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	view, err := h.ticketTypeUC.Update(ctx, organizer.ID, usecase.UpdateTicketTypeInput{
		TicketTypeID:    entity.TicketTypeID(req.GetTicketTypeId().GetValue()),
		Price:           req.GetPrice().GetValue(),
		Quantity:        int(req.GetQuantity()),
		PerAccountLimit: int(req.GetPerAccountLimit()),
	})
	if err != nil {
		return nil, err
	}
	return &tickettypev1.UpdateResponse{TicketType: mapper.TicketTypeToProto(view, true)}, nil
}
