// NOTE: This file requires a BSR module bump after liverty-music/specification PR #53 is merged.
// Remove the build tag below once go.mod is updated with the new BSR version that includes
// ticket/v1 and entry/v1 generated packages.
//
//go:build ignore

package rpc

import (
	"context"
	"errors"

	ticketv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket/v1"
	ticketconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket/v1/ticketv1connect"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time check that TicketHandler implements the generated service interface.
var _ ticketconnect.TicketServiceHandler = (*TicketHandler)(nil)

// TicketHandler implements the TicketService Connect interface.
type TicketHandler struct {
	ticketUseCase usecase.TicketUseCase
	logger        *logging.Logger
}

// NewTicketHandler creates a new ticket handler.
func NewTicketHandler(ticketUseCase usecase.TicketUseCase, logger *logging.Logger) *TicketHandler {
	return &TicketHandler{
		ticketUseCase: ticketUseCase,
		logger:        logger,
	}
}

// MintTicket mints a soulbound ticket for the authenticated user.
func (h *TicketHandler) MintTicket(
	ctx context.Context,
	req *connect.Request[ticketv1.MintTicketRequest],
) (*connect.Response[ticketv1.MintTicketResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	msg := req.Msg
	if msg.GetEventId() == nil || msg.GetEventId().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("event_id is required"))
	}

	if msg.GetTokenId() == nil || msg.GetTokenId().GetValue() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token_id must be greater than 0"))
	}

	ticket, err := h.ticketUseCase.MintTicket(ctx, &usecase.MintTicketParams{
		EventID:          msg.GetEventId().GetValue(),
		UserID:           claims.Sub, // internal user ID from JWT
		RecipientAddress: msg.GetRecipientAddress(),
		TokenID:          msg.GetTokenId().GetValue(),
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&ticketv1.MintTicketResponse{
		Ticket: mapper.TicketToProto(ticket),
	}), nil
}

// GetTicket retrieves a ticket by ID.
func (h *TicketHandler) GetTicket(
	ctx context.Context,
	req *connect.Request[ticketv1.GetTicketRequest],
) (*connect.Response[ticketv1.GetTicketResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	if req.Msg.GetTicketId() == nil || req.Msg.GetTicketId().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("ticket_id is required"))
	}

	ticket, err := h.ticketUseCase.GetTicket(ctx, req.Msg.GetTicketId().GetValue())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&ticketv1.GetTicketResponse{
		Ticket: mapper.TicketToProto(ticket),
	}), nil
}

// ListTicketsForUser retrieves all tickets for the authenticated user.
func (h *TicketHandler) ListTicketsForUser(
	ctx context.Context,
	req *connect.Request[ticketv1.ListTicketsForUserRequest],
) (*connect.Response[ticketv1.ListTicketsForUserResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	userID := req.Msg.GetUserId().GetValue()
	if userID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id is required"))
	}

	tickets, err := h.ticketUseCase.ListTicketsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&ticketv1.ListTicketsForUserResponse{
		Tickets: mapper.TicketsToProto(tickets),
	}), nil
}
