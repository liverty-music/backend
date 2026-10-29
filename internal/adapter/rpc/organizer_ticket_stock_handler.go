package rpc

import (
	"context"

	ticketstockv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/ticket_stock/v1/ticket_stockv1connect"
	ticketstockv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_stock/v1"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that OrganizerTicketStockHandler satisfies the
// generated interface.
var _ ticketstockv1connect.TicketStockServiceHandler = (*OrganizerTicketStockHandler)(nil)

// OrganizerTicketStockHandler implements the organizer-facing
// TicketStockService: a member stocks the events of their own Organizer with
// tickets of a seat type. The request is validated by the protovalidate
// interceptor before this handler runs; the handler resolves the caller's
// own Organizer and delegates to the use case.
type OrganizerTicketStockHandler struct {
	ticketStockUC usecase.TicketStockUseCase
	organizerUC   usecase.OrganizerUseCase
	logger        *logging.Logger
}

// NewOrganizerTicketStockHandler creates a new OrganizerTicketStockHandler.
func NewOrganizerTicketStockHandler(
	ticketStockUC usecase.TicketStockUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *OrganizerTicketStockHandler {
	return &OrganizerTicketStockHandler{ticketStockUC: ticketStockUC, organizerUC: organizerUC, logger: logger}
}

// Create stocks one of the caller's events with tickets of a seat type.
func (h *OrganizerTicketStockHandler) Create(
	ctx context.Context,
	req *ticketstockv1.CreateRequest,
) (*ticketstockv1.CreateResponse, error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	stock, err := h.ticketStockUC.Create(ctx, organizer.ID, req.GetEventId().GetValue(),
		entity.SeatTypeID(req.GetSeatTypeId().GetValue()), int(req.GetQuantity()))
	if err != nil {
		return nil, err
	}
	return &ticketstockv1.CreateResponse{TicketStock: mapper.TicketStockToProto(stock, true, false)}, nil
}

// Delete removes one of the caller's ticket stocks.
func (h *OrganizerTicketStockHandler) Delete(
	ctx context.Context,
	req *ticketstockv1.DeleteRequest,
) (*ticketstockv1.DeleteResponse, error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	if err := h.ticketStockUC.Delete(ctx, organizer.ID, entity.TicketStockID(req.GetTicketStockId().GetValue())); err != nil {
		return nil, err
	}
	return &ticketstockv1.DeleteResponse{}, nil
}

// ListByEvent returns the stocks of one of the caller's events with their
// sold and held counts, and their seat types.
func (h *OrganizerTicketStockHandler) ListByEvent(
	ctx context.Context,
	req *ticketstockv1.ListByEventRequest,
) (*ticketstockv1.ListByEventResponse, error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	stocks, err := h.ticketStockUC.ListByEvent(ctx, organizer.ID, req.GetEventId().GetValue())
	if err != nil {
		return nil, err
	}
	pbStocks, seatTypes := mapper.OwnTicketStocksToProto(stocks)
	return &ticketstockv1.ListByEventResponse{TicketStocks: pbStocks, SeatTypes: seatTypes}, nil
}
