package rpc

import (
	"context"
	"errors"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_journey/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// TicketJourneyHandler implements the TicketJourneyService Connect interface.
type TicketJourneyHandler struct {
	ticketJourneyUC usecase.TicketJourneyUseCase
	logger          *logging.Logger
}

// NewTicketJourneyHandler creates a new instance of the ticket journey RPC service handler.
func NewTicketJourneyHandler(
	ticketJourneyUC usecase.TicketJourneyUseCase,
	logger *logging.Logger,
) *TicketJourneyHandler {
	return &TicketJourneyHandler{
		ticketJourneyUC: ticketJourneyUC,
		logger:          logger,
	}
}

// SetStatus creates or updates the fan's ticket journey status for a specific event.
func (h *TicketJourneyHandler) SetStatus(ctx context.Context, req *connect.Request[rpc.SetStatusRequest]) (*connect.Response[rpc.SetStatusResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if req.Msg.EventId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("event_id is required"))
	}

	status, ok := mapper.TicketJourneyStatusFromProto[req.Msg.Status]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid ticket journey status"))
	}

	if err := h.ticketJourneyUC.SetStatus(ctx, userID, req.Msg.EventId.Value, status); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.SetStatusResponse{}), nil
}

// Delete removes the fan's ticket journey for a specific event.
func (h *TicketJourneyHandler) Delete(ctx context.Context, req *connect.Request[rpc.DeleteRequest]) (*connect.Response[rpc.DeleteResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if req.Msg.EventId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("event_id is required"))
	}

	if err := h.ticketJourneyUC.Delete(ctx, userID, req.Msg.EventId.Value); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.DeleteResponse{}), nil
}

// ListByUser retrieves all ticket journeys for the authenticated fan.
func (h *TicketJourneyHandler) ListByUser(ctx context.Context, _ *connect.Request[rpc.ListByUserRequest]) (*connect.Response[rpc.ListByUserResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	journeys, err := h.ticketJourneyUC.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.ListByUserResponse{
		Journeys: mapper.TicketJourneysToProto(journeys),
	}), nil
}
