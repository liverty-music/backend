package rpc

import (
	"context"
	"errors"

	organizerlotteryv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/lottery/v1/lotteryv1connect"
	organizerlotteryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/lottery/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that OrganizerLotteryHandler satisfies the generated
// interface.
var _ organizerlotteryv1connect.LotteryServiceHandler = (*OrganizerLotteryHandler)(nil)

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

// resolveCallerOrganizer reads the Zitadel org id from context and delegates
// to the usecase, which looks up the Organizer and enforces its lifecycle
// status. Returns the active Organizer or the usecase's error. Mirrors the
// same helper on OrganizerConcertHandler.
func (h *OrganizerLotteryHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}

	return h.organizerUC.ResolveCaller(ctx, callerOrgID)
}

// Configure attaches a new lottery sales phase to a published event
// owned by the caller's organizer.
func (h *OrganizerLotteryHandler) Configure(
	ctx context.Context,
	req *connect.Request[organizerlotteryv1.ConfigureRequest],
) (*connect.Response[organizerlotteryv1.ConfigureResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	in := usecase.ConfigureLotteryPhaseInput{
		CallerOrgID:              organizer.ID,
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

	return connect.NewResponse(&organizerlotteryv1.ConfigureResponse{
		Phase: mapper.LotterySalesPhaseToProto(phase),
	}), nil
}

// GetStatus returns the phase and its aggregate tallies.
func (h *OrganizerLotteryHandler) GetStatus(
	ctx context.Context,
	req *connect.Request[organizerlotteryv1.GetStatusRequest],
) (*connect.Response[organizerlotteryv1.GetStatusResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	phaseID := entity.LotteryPhaseID(req.Msg.GetPhaseId().GetValue())

	status, err := h.lotteryUC.GetLotteryPhaseStatus(ctx, phaseID, organizer.ID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerlotteryv1.GetStatusResponse{
		Phase:                      mapper.LotterySalesPhaseToProto(status.Phase),
		DrawCompleted:              status.DrawCompleted,
		ApplicationCount:           int32(status.ApplicationCount),
		RequestedTicketCount:       int32(status.RequestedTicketCount),
		WinningApplicationCount:    int32(status.WinningApplicationCount),
		WonTicketCount:             int32(status.WonTicketCount),
		WaitlistedApplicationCount: int32(status.WaitlistedApplicationCount),
	}), nil
}

// SetVerificationRequirement changes the identity-verification requirement
// on an existing lottery phase. The caller must be an active organizer
// (enforced by the OrgScopedInterceptor and resolveCallerOrganizer).
func (h *OrganizerLotteryHandler) SetVerificationRequirement(
	ctx context.Context,
	req *connect.Request[organizerlotteryv1.SetVerificationRequirementRequest],
) (*connect.Response[organizerlotteryv1.SetVerificationRequirementResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	in := usecase.SetVerificationRequirementInput{
		CallerOrgID:             organizer.ID,
		PhaseID:                 entity.LotteryPhaseID(req.Msg.GetPhaseId().GetValue()),
		VerificationRequirement: mapper.VerificationRequirementFromProto(req.Msg.GetVerificationRequirement()),
	}

	phase, err := h.lotteryUC.SetPhaseVerificationRequirement(ctx, in)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerlotteryv1.SetVerificationRequirementResponse{
		Phase: mapper.LotterySalesPhaseToProto(phase),
	}), nil
}
