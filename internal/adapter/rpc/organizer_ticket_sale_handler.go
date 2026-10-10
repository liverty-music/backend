package rpc

import (
	"context"
	"errors"

	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/ticket_sale/v1/ticket_salev1connect"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that OrganizerTicketSaleHandler satisfies the
// generated interface.
var _ ticketsalev1connect.TicketSaleServiceHandler = (*OrganizerTicketSaleHandler)(nil)

// OrganizerTicketSaleHandler implements the organizer-facing TicketSaleService.
// Org-scoped authorization is enforced by the OrgScopedInterceptor before this
// handler runs; the handler resolves the caller's own Organizer and delegates
// to the ticket sale use case.
type OrganizerTicketSaleHandler struct {
	ticketSaleUC usecase.TicketSaleUseCase
	organizerUC  usecase.OrganizerUseCase
	logger       *logging.Logger
}

// NewOrganizerTicketSaleHandler creates a new OrganizerTicketSaleHandler.
func NewOrganizerTicketSaleHandler(
	ticketSaleUC usecase.TicketSaleUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *OrganizerTicketSaleHandler {
	return &OrganizerTicketSaleHandler{ticketSaleUC: ticketSaleUC, organizerUC: organizerUC, logger: logger}
}

// resolveCallerOrganizer reads the Zitadel org id from context and delegates
// to OrganizerUseCase.ResolveCaller, whose failure is returned unchanged.
func (h *OrganizerTicketSaleHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	return h.organizerUC.ResolveCaller(ctx, callerOrgID)
}

// Configure creates or changes the sale of one of the caller's events.
func (h *OrganizerTicketSaleHandler) Configure(
	ctx context.Context,
	req *connect.Request[ticketsalev1.ConfigureRequest],
) (*connect.Response[ticketsalev1.ConfigureResponse], error) {
	msg := req.Msg
	eventID := msg.GetEventId().GetValue()
	if err := requireUUID("event_id", eventID); err != nil {
		return nil, err
	}
	if msg.GetSaleStartTime() == nil || msg.GetPrice() == 0 || msg.GetQuantity() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("sale_start_time, price and quantity are required"))
	}
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	in := usecase.ConfigureTicketSaleInput{
		EventID:   eventID,
		SaleStart: msg.GetSaleStartTime().AsTime(),
		Price:     msg.GetPrice(),
		Quantity:  int(msg.GetQuantity()),
	}
	if msg.GetSaleEndTime() != nil {
		end := msg.GetSaleEndTime().AsTime()
		in.SaleEnd = &end
	}
	if msg.PerAccountLimit != nil {
		limit := int(msg.GetPerAccountLimit())
		in.PerAccountLimit = &limit
	}

	view, err := h.ticketSaleUC.Configure(ctx, organizer.ID, in)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ticketsalev1.ConfigureResponse{TicketSale: mapper.TicketSaleToProto(view, true)}), nil
}

// Get returns the sale of one of the caller's events with its counts.
func (h *OrganizerTicketSaleHandler) Get(
	ctx context.Context,
	req *connect.Request[ticketsalev1.GetRequest],
) (*connect.Response[ticketsalev1.GetResponse], error) {
	eventID := req.Msg.GetEventId().GetValue()
	if err := requireUUID("event_id", eventID); err != nil {
		return nil, err
	}
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	view, err := h.ticketSaleUC.GetOwn(ctx, organizer.ID, eventID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ticketsalev1.GetResponse{TicketSale: mapper.TicketSaleToProto(view, true)}), nil
}
