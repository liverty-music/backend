package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminorderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/order/v1"
	lotteryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/lottery/v1"
	payoutonboardingv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/payout_onboarding/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// refundUCStub is a function-field RefundOrderUseCase that records its call.
type refundUCStub struct {
	called bool
	reason usecase.RefundReason
	now    time.Time
	fn     func(entity.OrderID) (*entity.Order, error)
}

func (s *refundUCStub) RefundOrder(_ context.Context, orderID entity.OrderID, reason usecase.RefundReason, now time.Time) (*entity.Order, error) {
	s.called, s.reason, s.now = true, reason, now
	return s.fn(orderID)
}

func newAdminOrderHandler(t *testing.T, uc usecase.RefundOrderUseCase) *handler.AdminOrderHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewAdminOrderHandler(uc, logger)
}

func TestAdminOrderHandler_Refund(t *testing.T) {
	t.Parallel()

	t.Run("admin refunds a cancelled event's order", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Admin refunds a cancelled event's order"
		uc := &refundUCStub{fn: func(id entity.OrderID) (*entity.Order, error) {
			return &entity.Order{ID: id, Status: entity.OrderStatusRefunded, Currency: "JPY"}, nil
		}}
		before := time.Now()
		resp, err := newAdminOrderHandler(t, uc).Refund(context.Background(), connect.NewRequest(&adminorderv1.RefundRequest{
			OrderId: &entityv1.OrderId{Value: "order-1"},
			Reason:  adminorderv1.RefundReason_REFUND_REASON_CANCELLATION,
		}))
		require.NoError(t, err)
		assert.Equal(t, usecase.RefundReasonCancellation, uc.reason)
		assert.False(t, uc.now.Before(before), "the current time is passed")
		assert.Equal(t, "order-1", resp.Msg.Order.Id.Value)
		assert.Equal(t, entityv1.OrderStatus_ORDER_STATUS_REFUNDED, resp.Msg.Order.Status)
	})

	t.Run("reason missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Reason missing"
		uc := &refundUCStub{}
		_, err := newAdminOrderHandler(t, uc).Refund(context.Background(), connect.NewRequest(&adminorderv1.RefundRequest{
			OrderId: &entityv1.OrderId{Value: "order-1"},
		}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		assert.False(t, uc.called, "nothing is refunded")
	})

	t.Run("unknown order", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Unknown order"
		uc := &refundUCStub{fn: func(entity.OrderID) (*entity.Order, error) {
			return nil, apperr.New(codes.NotFound, "order not found")
		}}
		_, err := newAdminOrderHandler(t, uc).Refund(context.Background(), connect.NewRequest(&adminorderv1.RefundRequest{
			OrderId: &entityv1.OrderId{Value: "order-x"},
			Reason:  adminorderv1.RefundReason_REFUND_REASON_CANCELLATION,
		}))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}

func TestLotteryHandler_CallerScenarios(t *testing.T) {
	t.Parallel()

	phase := &entityv1.LotterySalesPhaseId{Value: "phase-uuid-1"}

	t.Run("fan applies", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery "Fan applies"
		f := newLotteryHandlerFixture(t)
		f.seedUserRepo("ext-fan", "user-fan")
		var applicant entity.UserID
		f.lotteryUC.applyFn = func(_ context.Context, in usecase.ApplyInput) (*entity.TicketApplication, error) {
			applicant = in.ApplicantID
			return &entity.TicketApplication{ID: "app-1", PhaseID: in.PhaseID, ApplicantID: in.ApplicantID, State: entity.TicketApplicationStateApplied}, nil
		}
		_, err := f.h.Apply(lotteryAuthedCtx("ext-fan"), connect.NewRequest(&lotteryv1.ApplyRequest{
			PhaseId:              phase,
			RequestedTicketCount: 1,
			Identity:             &entityv1.HolderIdentity{FullName: "山田太郎", PhoneNumber: "+819012345678"},
			Authorization:        &entityv1.PaymentAuthorization{PaymentIntentRef: "pi_1"},
		}))
		require.NoError(t, err)
		assert.Equal(t, entity.UserID("user-fan"), applicant, "the applicant is the signed-in fan")
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery "Not signed in"
		f := newLotteryHandlerFixture(t)
		ran := false
		f.lotteryUC.getResultFn = func(context.Context, entity.LotteryPhaseID, entity.UserID) (*entity.TicketApplication, error) {
			ran = true
			return nil, nil
		}
		_, err := f.h.GetResult(context.Background(), connect.NewRequest(&lotteryv1.GetResultRequest{PhaseId: phase}))
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		assert.False(t, ran, "no usecase runs")
	})

	t.Run("caller without an account", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery "Caller without an account"
		f := newLotteryHandlerFixture(t)
		f.userRepo.EXPECT().GetByExternalID(mock.Anything, "ext-new").Return(nil, apperr.New(codes.NotFound, "no user"))
		applied := false
		f.lotteryUC.applyFn = func(context.Context, usecase.ApplyInput) (*entity.TicketApplication, error) {
			applied = true
			return nil, nil
		}
		_, err := f.h.Apply(lotteryAuthedCtx("ext-new"), connect.NewRequest(&lotteryv1.ApplyRequest{
			PhaseId:              phase,
			RequestedTicketCount: 1,
			Identity:             &entityv1.HolderIdentity{FullName: "山田太郎", PhoneNumber: "+819012345678"},
			Authorization:        &entityv1.PaymentAuthorization{PaymentIntentRef: "pi_1"},
		}))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.False(t, applied, "nothing is applied")
	})

	t.Run("withdraw own application", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery "Withdraw own application"
		f := newLotteryHandlerFixture(t)
		f.seedUserRepo("ext-fan", "user-fan")
		f.lotteryUC.getMyApplicationFn = func(_ context.Context, phaseID entity.LotteryPhaseID, applicant entity.UserID) (*entity.TicketApplication, error) {
			return &entity.TicketApplication{ID: "app-own", PhaseID: phaseID, ApplicantID: applicant, State: entity.TicketApplicationStateApplied}, nil
		}
		var withdrawn entity.TicketApplicationID
		var by entity.UserID
		f.lotteryUC.withdrawFn = func(_ context.Context, id entity.TicketApplicationID, applicant entity.UserID) error {
			withdrawn, by = id, applicant
			return nil
		}
		_, err := f.h.Withdraw(lotteryAuthedCtx("ext-fan"), connect.NewRequest(&lotteryv1.WithdrawRequest{PhaseId: phase}))
		require.NoError(t, err)
		assert.Equal(t, entity.TicketApplicationID("app-own"), withdrawn)
		assert.Equal(t, entity.UserID("user-fan"), by)
	})

	t.Run("no application", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery "No application"
		f := newLotteryHandlerFixture(t)
		f.seedUserRepo("ext-fan", "user-fan")
		f.lotteryUC.getMyApplicationFn = func(context.Context, entity.LotteryPhaseID, entity.UserID) (*entity.TicketApplication, error) {
			return nil, apperr.New(codes.NotFound, "no application")
		}
		withdrew := false
		f.lotteryUC.withdrawFn = func(context.Context, entity.TicketApplicationID, entity.UserID) error {
			withdrew = true
			return nil
		}
		_, err := f.h.Withdraw(lotteryAuthedCtx("ext-fan"), connect.NewRequest(&lotteryv1.WithdrawRequest{PhaseId: phase}))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.False(t, withdrew, "nothing is withdrawn")
	})
}

// onboardingUCStub is a function-field OnboardingUseCase.
type onboardingUCStub struct {
	fn func(organizerID string) (*entity.OrganizerConnectedAccount, string, error)
}

func (s *onboardingUCStub) GetOrCreateOnboarding(_ context.Context, organizerID string) (*entity.OrganizerConnectedAccount, string, error) {
	return s.fn(organizerID)
}

func TestPayoutOnboardingHandler_GetScenarios(t *testing.T) {
	t.Parallel()

	newHandler := func(t *testing.T, uc usecase.OnboardingUseCase) *handler.PayoutOnboardingHandler {
		t.Helper()
		organizerUC := ucmocks.NewMockOrganizerUseCase(t)
		organizerUC.EXPECT().ResolveCaller(mock.Anything, testZitadelOrgID).Return(&entity.Organizer{ID: "org-1"}, nil).Once()
		logger, err := logging.New()
		require.NoError(t, err)
		return handler.NewPayoutOnboardingHandler(uc, organizerUC, logger)
	}

	t.Run("operator opens payout settings", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/payout-onboarding "Operator opens payout settings"
		h := newHandler(t, &onboardingUCStub{fn: func(organizerID string) (*entity.OrganizerConnectedAccount, string, error) {
			return &entity.OrganizerConnectedAccount{OrganizerID: organizerID, AccountRef: "acct_1", Status: entity.PayoutOnboardingStatusPending},
				"https://connect.example/onboarding", nil
		}})
		resp, err := h.Get(orgCtx(testZitadelOrgID), connect.NewRequest(&payoutonboardingv1.GetRequest{}))
		require.NoError(t, err)
		assert.Equal(t, "org-1", resp.Msg.Account.OrganizerId.Value)
		assert.Equal(t, "acct_1", resp.Msg.Account.AccountRef)
		assert.Equal(t, "https://connect.example/onboarding", resp.Msg.OnboardingUrl, "an onboarding link while the account is not active")
	})

	t.Run("organizer gone meanwhile", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/payout-onboarding "Organizer gone meanwhile"
		h := newHandler(t, &onboardingUCStub{fn: func(string) (*entity.OrganizerConnectedAccount, string, error) {
			return nil, "", apperr.New(codes.NotFound, "organizer not found")
		}})
		_, err := h.Get(orgCtx(testZitadelOrgID), connect.NewRequest(&payoutonboardingv1.GetRequest{}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}
