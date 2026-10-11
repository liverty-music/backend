package rpc

import (
	"context"

	ticketv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket/v1/ticketv1connect"
	ticketv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket/v1"
	"connectrpc.com/connect"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that TicketHandler satisfies the generated interface.
var _ ticketv1connect.TicketServiceHandler = (*TicketHandler)(nil)

// TicketHandler implements the fan-facing TicketService: a signed-in fan
// lists the account-bound Tickets issued to them. The Ticket's user is always
// the caller; a request never names a User.
type TicketHandler struct {
	ticketUC usecase.TicketUseCase
	userUC   usecase.UserUseCase
	logger   *logging.Logger
}

// NewTicketHandler creates a new instance of the ticket RPC service handler.
func NewTicketHandler(ticketUC usecase.TicketUseCase, userUC usecase.UserUseCase, logger *logging.Logger) *TicketHandler {
	return &TicketHandler{ticketUC: ticketUC, userUC: userUC, logger: logger}
}

// List returns all account-bound tickets issued to the caller.
func (h *TicketHandler) List(ctx context.Context, _ *connect.Request[ticketv1.ListRequest]) (*connect.Response[ticketv1.ListResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	tickets, err := h.ticketUC.GetMyTickets(ctx, userID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ticketv1.ListResponse{Tickets: mapper.TicketsToProto(tickets)}), nil
}
