package rpc

import (
	"context"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_journey/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// TicketJourneyHandler implements the TicketJourneyService Connect interface.
type TicketJourneyHandler struct {
	ticketJourneyUC usecase.TicketJourneyUseCase
	userRepo        entity.UserRepository
	logger          *logging.Logger
}

// NewTicketJourneyHandler creates a new instance of the ticket journey RPC service handler.
func NewTicketJourneyHandler(
	ticketJourneyUC usecase.TicketJourneyUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *TicketJourneyHandler {
	return &TicketJourneyHandler{
		ticketJourneyUC: ticketJourneyUC,
		userRepo:        userRepo,
		logger:          logger,
	}
}

// SetStatus creates or updates the fan's ticket journey status for a specific event.
func (h *TicketJourneyHandler) SetStatus(ctx context.Context, req *connect.Request[rpc.SetStatusRequest]) (*connect.Response[rpc.SetStatusResponse], error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	status := mapper.TicketJourneyStatusFromProto[req.Msg.Status]

	// Resolve the internal users.id from the JWT sub claim (Zitadel provider_user_id).
	// ticket_journeys.user_id references users.id (internal UUID),
	// not the identity-provider-specific provider_user_id.
	user, err := h.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	if err := h.ticketJourneyUC.SetStatus(ctx, user.ID, req.Msg.EventId.Value, status); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.SetStatusResponse{}), nil
}

// Delete removes the fan's ticket journey for a specific event.
func (h *TicketJourneyHandler) Delete(ctx context.Context, req *connect.Request[rpc.DeleteRequest]) (*connect.Response[rpc.DeleteResponse], error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel provider_user_id).
	// ticket_journeys.user_id references users.id (internal UUID),
	// not the identity-provider-specific provider_user_id.
	user, err := h.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	if err := h.ticketJourneyUC.Delete(ctx, user.ID, req.Msg.EventId.Value); err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.DeleteResponse{}), nil
}

// List returns every ticket journey of the signed-in fan, empty when there is
// none.
func (h *TicketJourneyHandler) List(ctx context.Context, _ *connect.Request[rpc.ListRequest]) (*connect.Response[rpc.ListResponse], error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel provider_user_id).
	// ticket_journeys.user_id references users.id (internal UUID),
	// not the identity-provider-specific provider_user_id.
	user, err := h.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	journeys, err := h.ticketJourneyUC.List(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.ListResponse{
		Journeys: mapper.TicketJourneysToProto(journeys),
	}), nil
}
