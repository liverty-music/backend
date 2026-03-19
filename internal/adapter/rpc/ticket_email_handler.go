package rpc

import (
	"context"
	"errors"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_email/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// TicketEmailHandler implements the TicketEmailService Connect interface.
type TicketEmailHandler struct {
	ticketEmailUC usecase.TicketEmailUseCase
	logger        *logging.Logger
}

// NewTicketEmailHandler creates a new instance of the ticket email RPC service handler.
func NewTicketEmailHandler(
	ticketEmailUC usecase.TicketEmailUseCase,
	logger *logging.Logger,
) *TicketEmailHandler {
	return &TicketEmailHandler{
		ticketEmailUC: ticketEmailUC,
		logger:        logger,
	}
}

// CreateTicketEmail parses a shared email and persists the results.
func (h *TicketEmailHandler) CreateTicketEmail(ctx context.Context, req *connect.Request[rpc.CreateTicketEmailRequest]) (*connect.Response[rpc.CreateTicketEmailResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	emailType, ok := mapper.TicketEmailTypeFromProto[req.Msg.EmailType]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid email type"))
	}

	eventIDs := make([]string, 0, len(req.Msg.EventIds))
	for _, eid := range req.Msg.EventIds {
		if eid == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("event_id must not be nil"))
		}
		eventIDs = append(eventIDs, eid.Value)
	}

	emails, err := h.ticketEmailUC.Create(ctx, userID, eventIDs, emailType, req.Msg.RawBody)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.CreateTicketEmailResponse{
		TicketEmails: mapper.TicketEmailsToProto(emails),
	}), nil
}

// UpdateTicketEmail applies user corrections and triggers TicketJourney status updates.
func (h *TicketEmailHandler) UpdateTicketEmail(ctx context.Context, req *connect.Request[rpc.UpdateTicketEmailRequest]) (*connect.Response[rpc.UpdateTicketEmailResponse], error) {
	userID, ok := auth.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not authenticated"))
	}

	if req.Msg.TicketEmailId == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("ticket_email_id is required"))
	}

	params := &entity.UpdateTicketEmail{}

	if req.Msg.PaymentDeadline != nil {
		t := req.Msg.PaymentDeadline.AsTime()
		params.PaymentDeadline = &t
	}
	if req.Msg.LotteryStart != nil {
		t := req.Msg.LotteryStart.AsTime()
		params.LotteryStart = &t
	}
	if req.Msg.LotteryEnd != nil {
		t := req.Msg.LotteryEnd.AsTime()
		params.LotteryEnd = &t
	}
	if req.Msg.ApplicationUrl != nil {
		params.ApplicationURL = req.Msg.ApplicationUrl
	}
	if req.Msg.LotteryResult != nil {
		if r, ok := mapper.LotteryResultFromProto[*req.Msg.LotteryResult]; ok {
			params.LotteryResult = &r
		}
	}
	if req.Msg.PaymentStatus != nil {
		if s, ok := mapper.PaymentStatusFromProto[*req.Msg.PaymentStatus]; ok {
			params.PaymentStatus = &s
		}
	}

	updated, err := h.ticketEmailUC.Update(ctx, userID, req.Msg.TicketEmailId.Value, params)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.UpdateTicketEmailResponse{
		TicketEmail: mapper.TicketEmailToProto(updated),
	}), nil
}
