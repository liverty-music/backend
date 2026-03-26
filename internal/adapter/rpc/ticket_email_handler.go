package rpc

import (
	"context"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_email/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// TicketEmailHandler implements the TicketEmailService Connect interface.
type TicketEmailHandler struct {
	ticketEmailUC usecase.TicketEmailUseCase
	userRepo      entity.UserRepository
	logger        *logging.Logger
}

// NewTicketEmailHandler creates a new instance of the ticket email RPC service handler.
func NewTicketEmailHandler(
	ticketEmailUC usecase.TicketEmailUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *TicketEmailHandler {
	return &TicketEmailHandler{
		ticketEmailUC: ticketEmailUC,
		userRepo:      userRepo,
		logger:        logger,
	}
}

// CreateTicketEmail parses a shared email and persists the results.
func (h *TicketEmailHandler) CreateTicketEmail(ctx context.Context, req *connect.Request[rpc.CreateTicketEmailRequest]) (*connect.Response[rpc.CreateTicketEmailResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	emailType := mapper.TicketEmailTypeFromProto[req.Msg.EmailType]

	eventIDs := make([]string, 0, len(req.Msg.EventIds))
	for _, eid := range req.Msg.EventIds {
		eventIDs = append(eventIDs, eid.Value)
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	// ticket_emails.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id.
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	emails, err := h.ticketEmailUC.Create(ctx, user.ID, eventIDs, emailType, req.Msg.RawBody)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.CreateTicketEmailResponse{
		TicketEmails: mapper.TicketEmailsToProto(emails),
	}), nil
}

// UpdateTicketEmail applies user corrections and triggers TicketJourney status updates.
func (h *TicketEmailHandler) UpdateTicketEmail(ctx context.Context, req *connect.Request[rpc.UpdateTicketEmailRequest]) (*connect.Response[rpc.UpdateTicketEmailResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	// ticket_emails.user_id references users.id (internal UUID),
	// not the identity-provider-specific external_id.
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	params := &entity.UpdateTicketEmail{}

	if req.Msg.PaymentDeadline != nil {
		t := req.Msg.PaymentDeadline.AsTime()
		params.PaymentDeadlineTime = &t
	}
	if req.Msg.LotteryStart != nil {
		t := req.Msg.LotteryStart.AsTime()
		params.LotteryStartTime = &t
	}
	if req.Msg.LotteryEnd != nil {
		t := req.Msg.LotteryEnd.AsTime()
		params.LotteryEndTime = &t
	}
	if req.Msg.ApplicationUrl != nil {
		params.ApplicationURL = req.Msg.ApplicationUrl
	}
	if req.Msg.JourneyStatus != nil {
		if s, ok := mapper.JourneyStatusFromProto[*req.Msg.JourneyStatus]; ok {
			params.JourneyStatus = &s
		}
	}

	updated, err := h.ticketEmailUC.Update(ctx, user.ID, req.Msg.TicketEmailId.Value, params)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.UpdateTicketEmailResponse{
		TicketEmail: mapper.TicketEmailToProto(updated),
	}), nil
}
