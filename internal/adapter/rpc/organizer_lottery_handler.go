package rpc

import (
	"context"
	"errors"

	organizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/v1/organizerv1connect"
	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that OrganizerLotteryHandler satisfies the generated
// interface.
var _ organizerv1connect.LotteryServiceHandler = (*OrganizerLotteryHandler)(nil)

// OrganizerLotteryHandler implements the organizer-facing LotteryService
// Connect interface. Org-scoped authorization is enforced structurally by the
// OrgScopedInterceptor before this handler runs; this handler only resolves the
// caller's organizer and delegates to the lottery use case. No business logic
// lives here.
type OrganizerLotteryHandler struct {
	lotteryUC   usecase.LotteryUseCase
	organizerUC usecase.OrganizerUseCase
	logger      *logging.Logger
}

// NewOrganizerLotteryHandler creates a new OrganizerLotteryHandler.
func NewOrganizerLotteryHandler(
	lotteryUC usecase.LotteryUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *OrganizerLotteryHandler {
	return &OrganizerLotteryHandler{
		lotteryUC:   lotteryUC,
		organizerUC: organizerUC,
		logger:      logger,
	}
}

// resolveCallerOrganizer reads the Zitadel org id from context, looks up the
// Organizer, and enforces its lifecycle status. Returns the active Organizer or
// a Connect error. Mirrors the same helper on OrganizerConcertHandler.
func (h *OrganizerLotteryHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}

	organizer, err := h.organizerUC.GetByZitadelOrgID(ctx, callerOrgID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
		}
		return nil, err
	}

	switch organizer.Status {
	case entity.OrganizerStatusActive:
		return organizer, nil
	case entity.OrganizerStatusDeactivated:
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("organizer is deactivated"))
	default:
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
}

// ConfigureLotteryPhase attaches a new lottery sales phase to a published event
// owned by the caller's organizer.
func (h *OrganizerLotteryHandler) ConfigureLotteryPhase(
	ctx context.Context,
	req *connect.Request[organizerv1.ConfigureLotteryPhaseRequest],
) (*connect.Response[organizerv1.ConfigureLotteryPhaseResponse], error) {
	if _, err := h.resolveCallerOrganizer(ctx); err != nil {
		return nil, err
	}

	in := usecase.ConfigureLotteryPhaseInput{
		EventID:                  req.Msg.GetEventId().GetValue(),
		OpenTime:                 req.Msg.GetOpenTime().AsTime(),
		CloseTime:                req.Msg.GetCloseTime().AsTime(),
		TicketCapacity:           int(req.Msg.GetTicketCapacity()),
		MaxTicketsPerApplication: int(req.Msg.GetMaxTicketsPerApplication()),
		TicketPrice:              req.Msg.GetTicketPrice(),
		VerificationRequirement:  mapper.VerificationRequirementFromProto(req.Msg.GetVerificationRequirement()),
	}

	phase, err := h.lotteryUC.ConfigureLotteryPhase(ctx, in)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerv1.ConfigureLotteryPhaseResponse{
		Phase: mapper.LotterySalesPhaseToProto(phase),
	}), nil
}

// GetLotteryPhaseStatus returns the phase and its aggregate tallies.
func (h *OrganizerLotteryHandler) GetLotteryPhaseStatus(
	ctx context.Context,
	req *connect.Request[organizerv1.GetLotteryPhaseStatusRequest],
) (*connect.Response[organizerv1.GetLotteryPhaseStatusResponse], error) {
	if _, err := h.resolveCallerOrganizer(ctx); err != nil {
		return nil, err
	}

	phaseID := entity.LotteryPhaseID(req.Msg.GetPhaseId().GetValue())

	status, err := h.lotteryUC.GetLotteryPhaseStatus(ctx, phaseID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerv1.GetLotteryPhaseStatusResponse{
		Phase:                      mapper.LotterySalesPhaseToProto(status.Phase),
		DrawCompleted:              status.DrawCompleted,
		ApplicationCount:           int32(status.ApplicationCount),
		RequestedTicketCount:       int32(status.RequestedTicketCount),
		WinningApplicationCount:    int32(status.WinningApplicationCount),
		WonTicketCount:             int32(status.WonTicketCount),
		WaitlistedApplicationCount: int32(status.WaitlistedApplicationCount),
	}), nil
}

// SetPhaseVerificationRequirement changes the identity-verification requirement
// on an existing lottery phase. The caller must be an active organizer
// (enforced by the OrgScopedInterceptor and resolveCallerOrganizer).
func (h *OrganizerLotteryHandler) SetPhaseVerificationRequirement(
	ctx context.Context,
	req *connect.Request[organizerv1.SetPhaseVerificationRequirementRequest],
) (*connect.Response[organizerv1.SetPhaseVerificationRequirementResponse], error) {
	if _, err := h.resolveCallerOrganizer(ctx); err != nil {
		return nil, err
	}

	in := usecase.SetVerificationRequirementInput{
		PhaseID:                 entity.LotteryPhaseID(req.Msg.GetPhaseId().GetValue()),
		VerificationRequirement: mapper.VerificationRequirementFromProto(req.Msg.GetVerificationRequirement()),
	}

	phase, err := h.lotteryUC.SetPhaseVerificationRequirement(ctx, in)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerv1.SetPhaseVerificationRequirementResponse{
		Phase: mapper.LotterySalesPhaseToProto(phase),
	}), nil
}
