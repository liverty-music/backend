package rpc

import (
	"context"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// TicketHandler implements the TicketService Connect interface — the buyer-facing
// read surface over a caller's own Orders and issued Tickets.
type TicketHandler struct {
	ticketUC usecase.TicketUseCase
	userRepo entity.UserRepository
	logger   *logging.Logger
}

// NewTicketHandler creates a new instance of the ticket RPC service handler.
func NewTicketHandler(
	ticketUC usecase.TicketUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *TicketHandler {
	return &TicketHandler{
		ticketUC: ticketUC,
		userRepo: userRepo,
		logger:   logger,
	}
}

// GetOrder returns one of the caller's own Orders.
func (h *TicketHandler) GetOrder(ctx context.Context, req *connect.Request[rpc.GetOrderRequest]) (*connect.Response[rpc.GetOrderResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	order, err := h.ticketUC.GetOrder(ctx, entity.UserID(user.ID), entity.OrderID(req.Msg.OrderId.Value))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.GetOrderResponse{Order: mapper.OrderToProto(order)}), nil
}

// GetMyTickets returns all account-bound tickets issued to the caller.
func (h *TicketHandler) GetMyTickets(ctx context.Context, _ *connect.Request[rpc.GetMyTicketsRequest]) (*connect.Response[rpc.GetMyTicketsResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	tickets, err := h.ticketUC.GetMyTickets(ctx, entity.UserID(user.ID))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.GetMyTicketsResponse{Tickets: mapper.TicketsToProto(tickets)}), nil
}
