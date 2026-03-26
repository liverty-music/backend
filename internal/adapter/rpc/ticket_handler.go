package rpc

import (
	"context"
	"log/slog"

	ticketconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket/v1/ticketv1connect"
	ticketv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/blockchain/safe"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time check that TicketHandler implements the generated service interface.
var _ ticketconnect.TicketServiceHandler = (*TicketHandler)(nil)

// TicketHandler implements the TicketService Connect interface.
type TicketHandler struct {
	ticketUseCase usecase.TicketUseCase
	userRepo      entity.UserRepository
	safePredictor *safe.Predictor
	logger        *logging.Logger
}

// NewTicketHandler creates a new ticket handler.
func NewTicketHandler(ticketUseCase usecase.TicketUseCase, userRepo entity.UserRepository, safePredictor *safe.Predictor, logger *logging.Logger) *TicketHandler {
	return &TicketHandler{
		ticketUseCase: ticketUseCase,
		userRepo:      userRepo,
		safePredictor: safePredictor,
		logger:        logger,
	}
}

// MintTicket mints a soulbound ticket for the authenticated user.
func (h *TicketHandler) MintTicket(
	ctx context.Context,
	req *connect.Request[ticketv1.MintTicketRequest],
) (*connect.Response[ticketv1.MintTicketResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	msg := req.Msg

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	// This is required because tickets.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id.
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	// Lazily compute and persist the Safe address on first ticket mint.
	if user.SafeAddress == "" {
		addr := h.safePredictor.AddressHex(user.ID)
		if err := h.userRepo.UpdateSafeAddress(ctx, user.ID, addr); err != nil {
			h.logger.Warn(ctx, "failed to persist safe address, continuing with computed value",
				slog.String("user_id", user.ID),
				slog.String("error", err.Error()),
			)
		}
		user.SafeAddress = addr
	}

	ticket, err := h.ticketUseCase.MintTicket(ctx, &usecase.MintTicketParams{
		EventID:          msg.GetEventId().GetValue(),
		UserID:           user.ID,
		RecipientAddress: user.SafeAddress,
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
	ticket, err := h.ticketUseCase.GetTicket(ctx, req.Msg.GetTicketId().GetValue())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&ticketv1.GetTicketResponse{
		Ticket: mapper.TicketToProto(ticket),
	}), nil
}

// ListTickets retrieves all tickets for the authenticated user.
// The user ID is resolved from the JWT claims for authorization safety.
func (h *TicketHandler) ListTickets(
	ctx context.Context,
	_ *connect.Request[ticketv1.ListTicketsRequest],
) (*connect.Response[ticketv1.ListTicketsResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	// Use the authenticated user's internal ID from the resolved user record,
	// not the request body, to prevent users from listing other users' tickets.
	tickets, err := h.ticketUseCase.ListTicketsForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&ticketv1.ListTicketsResponse{
		Tickets: mapper.TicketsToProto(tickets),
	}), nil
}
