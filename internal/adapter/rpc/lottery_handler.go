package rpc

import (
	"context"

	lotteryv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/lottery/v1/lotteryv1connect"
	lotteryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/lottery/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that LotteryHandler satisfies the generated interface.
var _ lotteryv1connect.LotteryServiceHandler = (*LotteryHandler)(nil)

// LotteryHandler implements the fan-facing LotteryService Connect interface.
// It resolves the authenticated applicant via the JWT sub claim and delegates
// all business logic to the LotteryUseCase. No business logic lives here.
type LotteryHandler struct {
	lotteryUC usecase.LotteryUseCase
	userRepo  entity.UserRepository
	logger    *logging.Logger
}

// NewLotteryHandler creates a new LotteryHandler.
func NewLotteryHandler(
	lotteryUC usecase.LotteryUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *LotteryHandler {
	return &LotteryHandler{
		lotteryUC: lotteryUC,
		userRepo:  userRepo,
		logger:    logger,
	}
}

// resolveApplicantID extracts the JWT sub claim from context and maps it to the
// internal user ID via the user repository. Mirrors the pattern in FollowHandler.
func (h *LotteryHandler) resolveApplicantID(ctx context.Context) (entity.UserID, error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return "", err
	}

	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return "", err
	}

	return entity.UserID(user.ID), nil
}

// CreateAuthorization creates a Stripe manual-capture PaymentIntent for the
// requested phase and ticket count, placing an authorization hold on the fan's
// card.
func (h *LotteryHandler) CreateAuthorization(
	ctx context.Context,
	req *connect.Request[lotteryv1.CreateAuthorizationRequest],
) (*connect.Response[lotteryv1.CreateAuthorizationResponse], error) {
	// Caller identity is validated via the auth interceptor; only the internal
	// applicant ID is needed after this point for business operations. For
	// CreateAuthorization, the caller's identity is not persisted — this is just
	// a pre-apply step — so we only ensure the request is authenticated.
	if _, err := mapper.GetExternalUserID(ctx); err != nil {
		return nil, err
	}

	in := usecase.CreateAuthorizationInput{
		PhaseID:              entity.LotteryPhaseID(req.Msg.GetPhaseId().GetValue()),
		RequestedTicketCount: int(req.Msg.GetRequestedTicketCount()),
	}

	result, err := h.lotteryUC.CreateAuthorization(ctx, in)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&lotteryv1.CreateAuthorizationResponse{
		ClientSecret:     result.ClientSecret,
		PaymentIntentRef: result.PaymentIntentRef,
	}), nil
}

// Apply submits the caller's application to the given lottery phase.
func (h *LotteryHandler) Apply(
	ctx context.Context,
	req *connect.Request[lotteryv1.ApplyRequest],
) (*connect.Response[lotteryv1.ApplyResponse], error) {
	applicantID, err := h.resolveApplicantID(ctx)
	if err != nil {
		return nil, err
	}

	in := usecase.ApplyInput{
		PhaseID:              entity.LotteryPhaseID(req.Msg.GetPhaseId().GetValue()),
		ApplicantID:          applicantID,
		RequestedTicketCount: int(req.Msg.GetRequestedTicketCount()),
		Identity:             mapper.ApplicantIdentityFromProto(req.Msg.GetIdentity()),
		PaymentIntentRef:     req.Msg.GetAuthorization().GetPaymentIntentRef(),
	}

	app, err := h.lotteryUC.Apply(ctx, in)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&lotteryv1.ApplyResponse{
		Application: mapper.TicketApplicationToProto(app),
	}), nil
}

// WithdrawApplication cancels the caller's own active application for the
// given phase.
func (h *LotteryHandler) WithdrawApplication(
	ctx context.Context,
	req *connect.Request[lotteryv1.WithdrawApplicationRequest],
) (*connect.Response[lotteryv1.WithdrawApplicationResponse], error) {
	applicantID, err := h.resolveApplicantID(ctx)
	if err != nil {
		return nil, err
	}

	phaseID := entity.LotteryPhaseID(req.Msg.GetPhaseId().GetValue())

	// Resolve the caller's active application for the given phase, then withdraw
	// it. The usecase requires the application ID to enforce ownership; we look up
	// the application by (phase, applicant) first.
	app, err := h.lotteryUC.GetMyApplication(ctx, phaseID, applicantID)
	if err != nil {
		return nil, err
	}

	if err := h.lotteryUC.WithdrawApplication(ctx, app.ID, applicantID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&lotteryv1.WithdrawApplicationResponse{}), nil
}

// GetMyApplication returns the caller's active application for the given phase.
func (h *LotteryHandler) GetMyApplication(
	ctx context.Context,
	req *connect.Request[lotteryv1.GetMyApplicationRequest],
) (*connect.Response[lotteryv1.GetMyApplicationResponse], error) {
	applicantID, err := h.resolveApplicantID(ctx)
	if err != nil {
		return nil, err
	}

	phaseID := entity.LotteryPhaseID(req.Msg.GetPhaseId().GetValue())

	app, err := h.lotteryUC.GetMyApplication(ctx, phaseID, applicantID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&lotteryv1.GetMyApplicationResponse{
		Application: mapper.TicketApplicationToProto(app),
	}), nil
}

// GetResult returns the caller's draw result for the given phase.
// Returns FailedPrecondition when the draw has not yet run.
func (h *LotteryHandler) GetResult(
	ctx context.Context,
	req *connect.Request[lotteryv1.GetResultRequest],
) (*connect.Response[lotteryv1.GetResultResponse], error) {
	applicantID, err := h.resolveApplicantID(ctx)
	if err != nil {
		return nil, err
	}

	phaseID := entity.LotteryPhaseID(req.Msg.GetPhaseId().GetValue())

	app, err := h.lotteryUC.GetResult(ctx, phaseID, applicantID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&lotteryv1.GetResultResponse{
		Application: mapper.TicketApplicationToProto(app),
	}), nil
}
