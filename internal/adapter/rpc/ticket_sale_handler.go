package rpc

import (
	"context"

	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_sale/v1"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that TicketSaleHandler satisfies the generated
// interface.
var _ ticketsalev1connect.TicketSaleServiceHandler = (*TicketSaleHandler)(nil)

// TicketSaleHandler implements the fan-facing TicketSaleService: anyone,
// signed in or not, reads how an event's tickets are on sale. It never
// returns a TicketType's quantity, sold count or held count.
type TicketSaleHandler struct {
	ticketSaleUC usecase.TicketSaleUseCase
	logger       *logging.Logger
}

// NewTicketSaleHandler creates a new TicketSaleHandler.
func NewTicketSaleHandler(ticketSaleUC usecase.TicketSaleUseCase, logger *logging.Logger) *TicketSaleHandler {
	return &TicketSaleHandler{ticketSaleUC: ticketSaleUC, logger: logger}
}

// ListByEvent returns an event's sales in start order and the event's
// TicketStocks with their SeatTypes and states, without counts, and its
// per-account limit.
func (h *TicketSaleHandler) ListByEvent(
	ctx context.Context,
	req *ticketsalev1.ListByEventRequest,
) (*ticketsalev1.ListByEventResponse, error) {
	sales, err := h.ticketSaleUC.ListByEvent(ctx, req.GetEventId().GetValue())
	if err != nil {
		return nil, err
	}
	stocks, seatTypes := pb.FromFanTicketStocks(sales.Stocks)
	resp := &ticketsalev1.ListByEventResponse{
		TicketSales:  pb.FromTicketSales(sales.Sales),
		TicketStocks: stocks,
		SeatTypes:    seatTypes,
	}
	if sales.PerAccountLimit != nil {
		limit := int32(*sales.PerAccountLimit)
		resp.PerAccountLimit = &limit
	}
	return resp, nil
}
