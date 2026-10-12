package rpc

import (
	"context"
	"errors"

	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/ticket_sale/v1/ticket_salev1connect"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"
	"connectrpc.com/connect"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that OrganizerTicketSaleHandler satisfies the
// generated interface.
var _ ticketsalev1connect.TicketSaleServiceHandler = (*OrganizerTicketSaleHandler)(nil)

// OrganizerTicketSaleHandler implements the organizer-facing TicketSaleService:
// an operator opens and changes the sales of their own Organizer's Series.
// Org-scoped authorization is enforced by the OrgScopedInterceptor and the
// request by the protovalidate interceptor before this handler runs; the
// handler resolves the caller's own Organizer and delegates to the use case.
type OrganizerTicketSaleHandler struct {
	ticketSaleUC usecase.TicketSaleUseCase
	organizerUC  usecase.OrganizerUseCase
	logger       *logging.Logger
}

// NewOrganizerTicketSaleHandler creates a new OrganizerTicketSaleHandler.
func NewOrganizerTicketSaleHandler(
	ticketSaleUC usecase.TicketSaleUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *OrganizerTicketSaleHandler {
	return &OrganizerTicketSaleHandler{ticketSaleUC: ticketSaleUC, organizerUC: organizerUC, logger: logger}
}

// resolveCallerOrganizer returns the caller's own active Organizer. It fails
// with PermissionDenied when the caller has no tenant; a failure of
// OrganizerUseCase.ResolveCaller is returned unchanged.
func resolveCallerOrganizer(ctx context.Context, organizerUC usecase.OrganizerUseCase) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	return organizerUC.ResolveCaller(ctx, callerOrgID)
}

// Create opens a sale, with its ticket types, on events of one of the
// caller's Series.
func (h *OrganizerTicketSaleHandler) Create(
	ctx context.Context,
	req *connect.Request[ticketsalev1.CreateRequest],
) (*connect.Response[ticketsalev1.CreateResponse], error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	view, err := h.ticketSaleUC.Create(ctx, organizer.ID, mapper.CreateTicketSaleInputFromProto(req.Msg))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ticketsalev1.CreateResponse{TicketSale: mapper.TicketSaleToProto(view, true)}), nil
}

// ListByEvent returns the sales of one of the caller's events with their
// counts and, for a lottery, the entry stats.
func (h *OrganizerTicketSaleHandler) ListByEvent(
	ctx context.Context,
	req *connect.Request[ticketsalev1.ListByEventRequest],
) (*connect.Response[ticketsalev1.ListByEventResponse], error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	views, err := h.ticketSaleUC.ListOwnByEvent(ctx, organizer.ID, req.Msg.GetEventId().GetValue())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ticketsalev1.ListByEventResponse{
		TicketSales:       mapper.TicketSalesToProto(views, true),
		LotteryEntryStats: mapper.LotteryEntryStatsToProto(views),
	}), nil
}

// SetWindow moves the window of one of the caller's sales.
func (h *OrganizerTicketSaleHandler) SetWindow(
	ctx context.Context,
	req *connect.Request[ticketsalev1.SetWindowRequest],
) (*connect.Response[ticketsalev1.SetWindowResponse], error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	view, err := h.ticketSaleUC.SetWindow(ctx, organizer.ID,
		entity.TicketSaleID(req.Msg.GetTicketSaleId().GetValue()),
		req.Msg.GetSaleStartTime().AsTime(), req.Msg.GetSaleEndTime().AsTime())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ticketsalev1.SetWindowResponse{TicketSale: mapper.TicketSaleToProto(view, true)}), nil
}

// SetVerificationRequirement changes whether fans taking part in one of the
// caller's sales must hold an Active IdentityVerification; a request without one sets None.
func (h *OrganizerTicketSaleHandler) SetVerificationRequirement(
	ctx context.Context,
	req *connect.Request[ticketsalev1.SetVerificationRequirementRequest],
) (*connect.Response[ticketsalev1.SetVerificationRequirementResponse], error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	view, err := h.ticketSaleUC.SetVerificationRequirement(ctx, organizer.ID,
		entity.TicketSaleID(req.Msg.GetTicketSaleId().GetValue()),
		mapper.VerificationRequirementFromProto(req.Msg.GetVerificationRequirement()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ticketsalev1.SetVerificationRequirementResponse{TicketSale: mapper.TicketSaleToProto(view, true)}), nil
}
