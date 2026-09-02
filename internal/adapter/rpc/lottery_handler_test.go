package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	lotteryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/lottery/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// lotteryAuthedCtx returns a context with a JWT sub claim, as set by the authn
// middleware (fan-facing server).
func lotteryAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

// lotteryHandlerFixture wires a LotteryHandler with controllable stubs.
type lotteryHandlerFixture struct {
	h         *handler.LotteryHandler
	lotteryUC *handlerLotteryUCStub
	userRepo  *entitymocks.MockUserRepository
}

func newLotteryHandlerFixture(t *testing.T) *lotteryHandlerFixture {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	lotteryUC := &handlerLotteryUCStub{}
	userRepo := entitymocks.NewMockUserRepository(t)
	h := handler.NewLotteryHandler(lotteryUC, userRepo, logger)
	return &lotteryHandlerFixture{h: h, lotteryUC: lotteryUC, userRepo: userRepo}
}

// seedUserRepo configures the mock UserRepository to return a user with the
// given internal ID when looked up by external sub.
func (f *lotteryHandlerFixture) seedUserRepo(sub, internalID string) {
	f.userRepo.EXPECT().GetByExternalID(mock.Anything, sub).
		Return(&entity.User{ID: internalID}, nil).Once()
}

func TestLotteryHandler_CreateAuthorization(t *testing.T) {
	t.Parallel()

	validReq := &lotteryv1.CreateAuthorizationRequest{
		PhaseId:              &entityv1.LotterySalesPhaseId{Value: "phase-uuid-1"},
		RequestedTicketCount: 2,
	}

	tests := []struct {
		name     string
		ctx      context.Context
		req      *lotteryv1.CreateAuthorizationRequest
		setup    func(f *lotteryHandlerFixture)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success: returns client_secret and payment_intent_ref",
			ctx:  lotteryAuthedCtx("ext-user-1"),
			req:  validReq,
			// CreateAuthorization only validates auth; no userRepo call needed.
			setup:   func(_ *lotteryHandlerFixture) {},
			wantErr: false,
		},
		{
			name:     "error: unauthenticated",
			ctx:      context.Background(),
			req:      validReq,
			setup:    func(_ *lotteryHandlerFixture) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "error: phase not found propagates",
			ctx:  lotteryAuthedCtx("ext-user-1"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.lotteryUC.createAuthFn = func(_ context.Context, _ usecase.CreateAuthorizationInput) (usecase.CreateAuthorizationResult, error) {
					return usecase.CreateAuthorizationResult{}, apperr.New(apperr.ErrNotFound.Code, "no phase")
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newLotteryHandlerFixture(t)
			tt.setup(f)

			resp, err := f.h.CreateAuthorization(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.NotEmpty(t, resp.Msg.PaymentIntentRef)
			assert.NotEmpty(t, resp.Msg.ClientSecret)
		})
	}
}

func TestLotteryHandler_Apply(t *testing.T) {
	t.Parallel()

	validReq := &lotteryv1.ApplyRequest{
		PhaseId:              &entityv1.LotterySalesPhaseId{Value: "phase-uuid-1"},
		RequestedTicketCount: 2,
		Identity: &entityv1.ApplicantIdentity{
			FullName:    "山田太郎",
			PhoneNumber: "+819012345678",
		},
		Authorization: &entityv1.PaymentAuthorization{
			PaymentIntentRef: "pi_test_ref",
		},
	}

	tests := []struct {
		name     string
		ctx      context.Context
		req      *lotteryv1.ApplyRequest
		setup    func(f *lotteryHandlerFixture)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success: returns application in Applied state",
			ctx:  lotteryAuthedCtx("ext-user-1"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-1", "internal-user-1")
			},
			wantErr: false,
		},
		{
			name:     "error: unauthenticated",
			ctx:      context.Background(),
			req:      validReq,
			setup:    func(_ *lotteryHandlerFixture) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "error: window closed propagates FailedPrecondition from usecase",
			ctx:  lotteryAuthedCtx("ext-user-2"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-2", "internal-user-2")
				f.lotteryUC.applyFn = func(_ context.Context, _ usecase.ApplyInput) (*entity.TicketApplication, error) {
					return nil, apperr.New(apperr.ErrFailedPrecondition.Code, "window closed")
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newLotteryHandlerFixture(t)
			tt.setup(f)

			resp, err := f.h.Apply(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.NotNil(t, resp.Msg.Application)
			assert.Equal(t, entityv1.TicketApplicationState_TICKET_APPLICATION_STATE_APPLIED, resp.Msg.Application.GetState())
		})
	}
}

func TestLotteryHandler_WithdrawApplication(t *testing.T) {
	t.Parallel()

	validReq := &lotteryv1.WithdrawApplicationRequest{
		PhaseId: &entityv1.LotterySalesPhaseId{Value: "phase-uuid-1"},
	}

	tests := []struct {
		name     string
		ctx      context.Context
		req      *lotteryv1.WithdrawApplicationRequest
		setup    func(f *lotteryHandlerFixture)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success: withdraws active application",
			ctx:  lotteryAuthedCtx("ext-user-1"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-1", "internal-user-1")
			},
			wantErr: false,
		},
		{
			name:     "error: unauthenticated",
			ctx:      context.Background(),
			req:      validReq,
			setup:    func(_ *lotteryHandlerFixture) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "error: no active application returns error",
			ctx:  lotteryAuthedCtx("ext-user-3"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-3", "internal-user-3")
				f.lotteryUC.getMyApplicationFn = func(_ context.Context, _ entity.LotteryPhaseID, _ entity.UserID) (*entity.TicketApplication, error) {
					return nil, apperr.New(apperr.ErrNotFound.Code, "no application")
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newLotteryHandlerFixture(t)
			tt.setup(f)

			resp, err := f.h.WithdrawApplication(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
		})
	}
}

func TestLotteryHandler_GetMyApplication(t *testing.T) {
	t.Parallel()

	validReq := &lotteryv1.GetMyApplicationRequest{
		PhaseId: &entityv1.LotterySalesPhaseId{Value: "phase-uuid-1"},
	}

	tests := []struct {
		name     string
		ctx      context.Context
		req      *lotteryv1.GetMyApplicationRequest
		setup    func(f *lotteryHandlerFixture)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success: returns active application",
			ctx:  lotteryAuthedCtx("ext-user-1"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-1", "internal-user-1")
			},
			wantErr: false,
		},
		{
			name:     "error: unauthenticated",
			ctx:      context.Background(),
			req:      validReq,
			setup:    func(_ *lotteryHandlerFixture) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "error: no application returns error",
			ctx:  lotteryAuthedCtx("ext-user-2"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-2", "internal-user-2")
				f.lotteryUC.getMyApplicationFn = func(_ context.Context, _ entity.LotteryPhaseID, _ entity.UserID) (*entity.TicketApplication, error) {
					return nil, apperr.New(apperr.ErrNotFound.Code, "no application")
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newLotteryHandlerFixture(t)
			tt.setup(f)

			resp, err := f.h.GetMyApplication(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.NotNil(t, resp.Msg.Application)
		})
	}
}

func TestLotteryHandler_GetResult(t *testing.T) {
	t.Parallel()

	validReq := &lotteryv1.GetResultRequest{
		PhaseId: &entityv1.LotterySalesPhaseId{Value: "phase-uuid-1"},
	}

	tests := []struct {
		name  string
		ctx   context.Context
		req   *lotteryv1.GetResultRequest
		setup func(f *lotteryHandlerFixture)
		// wantCode is non-zero only for errors that surface as *connect.Error
		// (i.e. from mapper helpers like GetExternalUserID). Errors propagated
		// directly from the usecase are raw apperr values; use wantApperr for those.
		wantCode   connect.Code
		wantApperr error
		wantErr    bool
		wantState  entityv1.TicketApplicationState
	}{
		{
			name: "success: returns Won result after draw",
			ctx:  lotteryAuthedCtx("ext-user-1"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-1", "internal-user-1")
				// Default stub returns Won state.
			},
			wantErr:   false,
			wantState: entityv1.TicketApplicationState_TICKET_APPLICATION_STATE_WON,
		},
		{
			name: "success: returns Lost result after draw",
			ctx:  lotteryAuthedCtx("ext-user-2"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-2", "internal-user-2")
				f.lotteryUC.getResultFn = func(_ context.Context, phaseID entity.LotteryPhaseID, applicantID entity.UserID) (*entity.TicketApplication, error) {
					return &entity.TicketApplication{
						ID:          "app-uuid-2",
						PhaseID:     phaseID,
						ApplicantID: applicantID,
						State:       entity.TicketApplicationStateLost,
					}, nil
				}
			},
			wantErr:   false,
			wantState: entityv1.TicketApplicationState_TICKET_APPLICATION_STATE_LOST,
		},
		{
			name:     "error: unauthenticated",
			ctx:      context.Background(),
			req:      validReq,
			setup:    func(_ *lotteryHandlerFixture) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "error: draw not run returns FailedPrecondition apperr",
			ctx:  lotteryAuthedCtx("ext-user-3"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-3", "internal-user-3")
				f.lotteryUC.getResultFn = func(_ context.Context, _ entity.LotteryPhaseID, _ entity.UserID) (*entity.TicketApplication, error) {
					return nil, apperr.New(apperr.ErrFailedPrecondition.Code, "draw has not run yet")
				}
			},
			wantApperr: apperr.ErrFailedPrecondition,
			wantErr:    true,
		},
		{
			name: "error: no application returns NotFound apperr",
			ctx:  lotteryAuthedCtx("ext-user-4"),
			req:  validReq,
			setup: func(f *lotteryHandlerFixture) {
				f.seedUserRepo("ext-user-4", "internal-user-4")
				f.lotteryUC.getResultFn = func(_ context.Context, _ entity.LotteryPhaseID, _ entity.UserID) (*entity.TicketApplication, error) {
					return nil, apperr.New(apperr.ErrNotFound.Code, "no application")
				}
			},
			wantApperr: apperr.ErrNotFound,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newLotteryHandlerFixture(t)
			tt.setup(f)

			resp, err := f.h.GetResult(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				if tt.wantApperr != nil {
					assert.ErrorIs(t, err, tt.wantApperr)
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.NotNil(t, resp.Msg.Application)
			assert.Equal(t, tt.wantState, resp.Msg.Application.GetState())
		})
	}
}
