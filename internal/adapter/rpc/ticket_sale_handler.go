package rpc

import (
	"context"

	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_sale/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that TicketSaleHandler satisfies the generated
// interface.
var _ ticketsalev1connect.TicketSaleServiceHandler = (*TicketSaleHandler)(nil)

// TicketSaleHandler implements the fan-facing TicketSaleService: anyone,
// signed in or not, reads how an event's tickets are on sale. It never
// returns the quantity or the sold count.
type TicketSaleHandler struct {
	ticketSaleUC usecase.TicketSaleUseCase
	logger       *logging.Logger
}

// NewTicketSaleHandler creates a new TicketSaleHandler.
func NewTicketSaleHandler(ticketSaleUC usecase.TicketSaleUseCase, logger *logging.Logger) *TicketSaleHandler {
	return &TicketSaleHandler{ticketSaleUC: ticketSaleUC, logger: logger}
}

// Get returns an event's sale with its state now and whether stock is low.
func (h *TicketSaleHandler) Get(
	ctx context.Context,
	req *connect.Request[ticketsalev1.GetRequest],
) (*connect.Response[ticketsalev1.GetResponse], error) {
	eventID := req.Msg.GetEventId().GetValue()
	if err := requireUUID("event_id", eventID); err != nil {
		return nil, err
	}
	view, err := h.ticketSaleUC.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ticketsalev1.GetResponse{
		TicketSale:    mapper.TicketSaleToProto(view.Sale, false),
		State:         mapper.TicketSaleStateToProto(view.State),
		LowStock:      view.LowStock,
		SellerDetails: mapper.SellerDetailsToProto(view.SellerDetails),
	}), nil
}
