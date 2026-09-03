package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// orgLotteryAuthedCtx returns a context with a resolved Zitadel org id, as set
// by OrgScopedInterceptor.
func orgLotteryAuthedCtx(zitadelOrgID string) context.Context {
	return auth.WithCallerOrgID(context.Background(), zitadelOrgID)
}

// handlerLotteryUCStub is a function-field stub implementing usecase.LotteryUseCase.
// Mockery v2.53.6 crashes with "package image without types" when generating
// mocks for packages that transitively import the entity package; these stubs
// mirror the approach used in lottery_uc_test.go.
type handlerLotteryUCStub struct {
	configurePhaseFn             func(context.Context, usecase.ConfigureLotteryPhaseInput) (*entity.LotterySalesPhase, error)
	createAuthFn                 func(context.Context, usecase.CreateAuthorizationInput) (usecase.CreateAuthorizationResult, error)
	applyFn                      func(context.Context, usecase.ApplyInput) (*entity.TicketApplication, error)
	withdrawFn                   func(context.Context, entity.TicketApplicationID, entity.UserID) error
	getMyApplicationFn           func(context.Context, entity.LotteryPhaseID, entity.UserID) (*entity.TicketApplication, error)
	getResultFn                  func(context.Context, entity.LotteryPhaseID, entity.UserID) (*entity.TicketApplication, error)
	getLotteryStatusFn           func(context.Context, entity.LotteryPhaseID) (*entity.LotteryPhaseStatus, error)
	setVerificationRequirementFn func(context.Context, usecase.SetVerificationRequirementInput) (*entity.LotterySalesPhase, error)
}

var _ usecase.LotteryUseCase = (*handlerLotteryUCStub)(nil)

func (s *handlerLotteryUCStub) ConfigureLotteryPhase(ctx context.Context, in usecase.ConfigureLotteryPhaseInput) (*entity.LotterySalesPhase, error) {
	if s.configurePhaseFn != nil {
		return s.configurePhaseFn(ctx, in)
	}
	return &entity.LotterySalesPhase{ID: "phase-uuid-1", EventID: in.EventID}, nil
}

func (s *handlerLotteryUCStub) CreateAuthorization(ctx context.Context, in usecase.CreateAuthorizationInput) (usecase.CreateAuthorizationResult, error) {
	if s.createAuthFn != nil {
		return s.createAuthFn(ctx, in)
	}
	return usecase.CreateAuthorizationResult{PaymentIntentRef: "pi_test", ClientSecret: "cs_test"}, nil
}

func (s *handlerLotteryUCStub) Apply(ctx context.Context, in usecase.ApplyInput) (*entity.TicketApplication, error) {
	if s.applyFn != nil {
		return s.applyFn(ctx, in)
	}
	return &entity.TicketApplication{ID: "app-uuid-1", State: entity.TicketApplicationStateApplied}, nil
}

func (s *handlerLotteryUCStub) WithdrawApplication(ctx context.Context, id entity.TicketApplicationID, applicantID entity.UserID) error {
	if s.withdrawFn != nil {
		return s.withdrawFn(ctx, id, applicantID)
	}
	return nil
}

func (s *handlerLotteryUCStub) GetMyApplication(ctx context.Context, phaseID entity.LotteryPhaseID, applicantID entity.UserID) (*entity.TicketApplication, error) {
	if s.getMyApplicationFn != nil {
		return s.getMyApplicationFn(ctx, phaseID, applicantID)
	}
	return &entity.TicketApplication{ID: "app-uuid-1", PhaseID: phaseID, ApplicantID: applicantID, State: entity.TicketApplicationStateApplied}, nil
}

func (s *handlerLotteryUCStub) GetResult(ctx context.Context, phaseID entity.LotteryPhaseID, applicantID entity.UserID) (*entity.TicketApplication, error) {
	if s.getResultFn != nil {
		return s.getResultFn(ctx, phaseID, applicantID)
	}
	return &entity.TicketApplication{ID: "app-uuid-1", PhaseID: phaseID, ApplicantID: applicantID, State: entity.TicketApplicationStateWon}, nil
}

func (s *handlerLotteryUCStub) GetLotteryPhaseStatus(ctx context.Context, phaseID entity.LotteryPhaseID) (*entity.LotteryPhaseStatus, error) {
	if s.getLotteryStatusFn != nil {
		return s.getLotteryStatusFn(ctx, phaseID)
	}
	return &entity.LotteryPhaseStatus{
		Phase: &entity.LotterySalesPhase{ID: phaseID, EventID: "event-1"},
	}, nil
}

// DrawDuePhases and RunDraw are part of LotteryUseCase but are exercised by the
// draw sweeper, not the RPC handlers; the stub implements them as no-ops so it
// satisfies the interface.
func (s *handlerLotteryUCStub) DrawDuePhases(ctx context.Context, now time.Time) error {
	return nil
}

func (s *handlerLotteryUCStub) RunDraw(ctx context.Context, phaseID entity.LotteryPhaseID) error {
	return nil
}

func (s *handlerLotteryUCStub) SetPhaseVerificationRequirement(ctx context.Context, in usecase.SetVerificationRequirementInput) (*entity.LotterySalesPhase, error) {
	if s.setVerificationRequirementFn != nil {
		return s.setVerificationRequirementFn(ctx, in)
	}
	return &entity.LotterySalesPhase{ID: in.PhaseID, VerificationRequirement: in.VerificationRequirement}, nil
}

// activeOrganizerWithID returns an active organizer with the given ID.
func activeOrganizerWithID(id string) *entity.Organizer {
	return &entity.Organizer{ID: id, Status: entity.OrganizerStatusActive}
}

func TestOrganizerLotteryHandler_ConfigureLotteryPhase(t *testing.T) {
	t.Parallel()

	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	close := open.Add(7 * 24 * time.Hour)

	validReq := &organizerv1.ConfigureLotteryPhaseRequest{
		EventId:                  &entityv1.EventId{Value: "event-1"},
		OpenTime:                 timestamppb.New(open),
		CloseTime:                timestamppb.New(close),
		TicketCapacity:           100,
		MaxTicketsPerApplication: 4,
		TicketPrice:              5000,
	}

	tests := []struct {
		name         string
		ctx          context.Context
		req          *organizerv1.ConfigureLotteryPhaseRequest
		setup        func(uc *ucmocks.MockOrganizerUseCase)
		lotterySetup func(stub *handlerLotteryUCStub)
		wantCode     connect.Code
		wantErr      bool
	}{
		{
			name: "success: returns created phase",
			ctx:  orgLotteryAuthedCtx("org-1"),
			req:  validReq,
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-1").
					Return(activeOrganizerWithID("organizer-uuid-1"), nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error: unauthenticated — no org id in context",
			ctx:      context.Background(),
			req:      validReq,
			setup:    func(_ *ucmocks.MockOrganizerUseCase) {},
			wantCode: connect.CodePermissionDenied,
			wantErr:  true,
		},
		{
			name: "error: organizer not found returns PermissionDenied",
			ctx:  orgLotteryAuthedCtx("org-unknown"),
			req:  validReq,
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-unknown").
					Return(nil, apperr.New(apperr.ErrNotFound.Code, "not found")).Once()
			},
			wantCode: connect.CodePermissionDenied,
			wantErr:  true,
		},
		{
			name: "error: deactivated organizer returns FailedPrecondition",
			ctx:  orgLotteryAuthedCtx("org-deactivated"),
			req:  validReq,
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-deactivated").
					Return(&entity.Organizer{ID: "org-uuid", Status: entity.OrganizerStatusDeactivated}, nil).Once()
			},
			wantCode: connect.CodeFailedPrecondition,
			wantErr:  true,
		},
		{
			name: "error: event not published propagates FailedPrecondition from usecase",
			ctx:  orgLotteryAuthedCtx("org-1"),
			req:  validReq,
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-1").
					Return(activeOrganizerWithID("organizer-uuid-1"), nil).Once()
			},
			lotterySetup: func(stub *handlerLotteryUCStub) {
				stub.configurePhaseFn = func(_ context.Context, _ usecase.ConfigureLotteryPhaseInput) (*entity.LotterySalesPhase, error) {
					return nil, apperr.New(apperr.ErrFailedPrecondition.Code, "event is not published")
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			organizerUC := ucmocks.NewMockOrganizerUseCase(t)
			tt.setup(organizerUC)

			lotteryUC := &handlerLotteryUCStub{}
			if tt.lotterySetup != nil {
				tt.lotterySetup(lotteryUC)
			}

			h := handler.NewOrganizerLotteryHandler(lotteryUC, organizerUC, logger)
			resp, err := h.ConfigureLotteryPhase(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.NotNil(t, resp.Msg.Phase)
			assert.Equal(t, "phase-uuid-1", resp.Msg.Phase.GetId().GetValue())
		})
	}
}

func TestOrganizerLotteryHandler_GetLotteryPhaseStatus(t *testing.T) {
	t.Parallel()

	validReq := &organizerv1.GetLotteryPhaseStatusRequest{
		PhaseId: &entityv1.LotterySalesPhaseId{Value: "phase-uuid-1"},
	}

	tests := []struct {
		name         string
		ctx          context.Context
		req          *organizerv1.GetLotteryPhaseStatusRequest
		setup        func(uc *ucmocks.MockOrganizerUseCase)
		lotterySetup func(stub *handlerLotteryUCStub)
		wantCode     connect.Code
		wantErr      bool
		wantDraw     bool
	}{
		{
			name: "success: returns phase and zero tallies (draw not run)",
			ctx:  orgLotteryAuthedCtx("org-1"),
			req:  validReq,
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-1").
					Return(activeOrganizerWithID("organizer-uuid-1"), nil).Once()
			},
			wantErr:  false,
			wantDraw: false,
		},
		{
			name: "success: draw_completed true when draw has run",
			ctx:  orgLotteryAuthedCtx("org-1"),
			req:  validReq,
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-1").
					Return(activeOrganizerWithID("organizer-uuid-1"), nil).Once()
			},
			lotterySetup: func(stub *handlerLotteryUCStub) {
				stub.getLotteryStatusFn = func(_ context.Context, phaseID entity.LotteryPhaseID) (*entity.LotteryPhaseStatus, error) {
					return &entity.LotteryPhaseStatus{
						Phase:                      &entity.LotterySalesPhase{ID: phaseID, EventID: "event-1"},
						DrawCompleted:              true,
						ApplicationCount:           10,
						RequestedTicketCount:       25,
						WinningApplicationCount:    5,
						WonTicketCount:             12,
						WaitlistedApplicationCount: 5,
					}, nil
				}
			},
			wantErr:  false,
			wantDraw: true,
		},
		{
			name:     "error: unauthenticated",
			ctx:      context.Background(),
			req:      validReq,
			setup:    func(_ *ucmocks.MockOrganizerUseCase) {},
			wantCode: connect.CodePermissionDenied,
			wantErr:  true,
		},
		{
			name: "error: phase not found propagates",
			ctx:  orgLotteryAuthedCtx("org-1"),
			req:  validReq,
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-1").
					Return(activeOrganizerWithID("organizer-uuid-1"), nil).Once()
			},
			lotterySetup: func(stub *handlerLotteryUCStub) {
				stub.getLotteryStatusFn = func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotteryPhaseStatus, error) {
					return nil, apperr.New(apperr.ErrNotFound.Code, "no phase")
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			organizerUC := ucmocks.NewMockOrganizerUseCase(t)
			tt.setup(organizerUC)

			lotteryUC := &handlerLotteryUCStub{}
			if tt.lotterySetup != nil {
				tt.lotterySetup(lotteryUC)
			}

			h := handler.NewOrganizerLotteryHandler(lotteryUC, organizerUC, logger)
			resp, err := h.GetLotteryPhaseStatus(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.NotNil(t, resp.Msg.Phase)
			assert.Equal(t, tt.wantDraw, resp.Msg.DrawCompleted)
		})
	}
}

func TestOrganizerLotteryHandler_SetPhaseVerificationRequirement(t *testing.T) {
	t.Parallel()

	phaseID := &entityv1.LotterySalesPhaseId{Value: "phase-uuid-1"}

	tests := []struct {
		name         string
		ctx          context.Context
		req          *organizerv1.SetPhaseVerificationRequirementRequest
		setup        func(uc *ucmocks.MockOrganizerUseCase)
		lotterySetup func(stub *handlerLotteryUCStub)
		wantCode     connect.Code
		wantErr      bool
		wantReq      entityv1.VerificationRequirement
	}{
		{
			name: "success: owner sets JPKI_ONLY — returns updated phase",
			ctx:  orgLotteryAuthedCtx("org-1"),
			req: &organizerv1.SetPhaseVerificationRequirementRequest{
				PhaseId:                 phaseID,
				VerificationRequirement: entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY,
			},
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-1").
					Return(activeOrganizerWithID("organizer-uuid-1"), nil).Once()
			},
			wantReq: entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY,
		},
		{
			name: "success: owner sets VERIFIED_ANY — returns updated phase",
			ctx:  orgLotteryAuthedCtx("org-1"),
			req: &organizerv1.SetPhaseVerificationRequirementRequest{
				PhaseId:                 phaseID,
				VerificationRequirement: entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_VERIFIED_ANY,
			},
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-1").
					Return(activeOrganizerWithID("organizer-uuid-1"), nil).Once()
			},
			wantReq: entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_VERIFIED_ANY,
		},
		{
			name: "error: unauthenticated — no org id in context returns PermissionDenied",
			ctx:  context.Background(),
			req: &organizerv1.SetPhaseVerificationRequirementRequest{
				PhaseId:                 phaseID,
				VerificationRequirement: entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY,
			},
			setup:    func(_ *ucmocks.MockOrganizerUseCase) {},
			wantCode: connect.CodePermissionDenied,
			wantErr:  true,
		},
		{
			name: "error: non-owner organizer returns PermissionDenied",
			ctx:  orgLotteryAuthedCtx("org-other"),
			req: &organizerv1.SetPhaseVerificationRequirementRequest{
				PhaseId:                 phaseID,
				VerificationRequirement: entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY,
			},
			setup: func(uc *ucmocks.MockOrganizerUseCase) {
				uc.EXPECT().GetByZitadelOrgID(mock.Anything, "org-other").
					Return(nil, apperr.New(apperr.ErrNotFound.Code, "not found")).Once()
			},
			wantCode: connect.CodePermissionDenied,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			organizerUC := ucmocks.NewMockOrganizerUseCase(t)
			tt.setup(organizerUC)

			lotteryUC := &handlerLotteryUCStub{}
			if tt.lotterySetup != nil {
				tt.lotterySetup(lotteryUC)
			}

			h := handler.NewOrganizerLotteryHandler(lotteryUC, organizerUC, logger)
			resp, err := h.SetPhaseVerificationRequirement(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.NotNil(t, resp.Msg.Phase)
			assert.Equal(t, tt.wantReq, resp.Msg.Phase.GetVerificationRequirement())
		})
	}
}
