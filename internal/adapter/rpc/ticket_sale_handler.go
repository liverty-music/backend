package rpc

import (
	"context"

	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_sale/v1"
	"connectrpc.com/connect"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
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

// ListByEvent returns an event's sales in start order, each with its
// TicketType for the event and, for a first-come one, its state now.
func (h *TicketSaleHandler) ListByEvent(
	ctx context.Context,
	req *connect.Request[ticketsalev1.ListByEventRequest],
) (*connect.Response[ticketsalev1.ListByEventResponse], error) {
	views, err := h.ticketSaleUC.ListByEvent(ctx, req.Msg.GetEventId().GetValue())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ticketsalev1.ListByEventResponse{TicketSales: mapper.TicketSalesToProto(views, false)}), nil
}
