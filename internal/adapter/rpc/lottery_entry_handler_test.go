package rpc_test

import (
	"context"
	"net/http"
	"testing"

	lotteryentryv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/lottery_entry/v1/lottery_entryv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	lotteryentryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/lottery_entry/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// lotteryEntryFixture serves a LotteryEntryHandler behind the validation
// interceptor. The usecase mocks fail the test on any call not expected.
type lotteryEntryFixture struct {
	lottery *ucmocks.MockLotteryUseCase
	users   *ucmocks.MockUserUseCase
	client  lotteryentryv1connect.LotteryEntryServiceClient
}

func newLotteryEntryFixture(t *testing.T, decorate func(context.Context) context.Context) *lotteryEntryFixture {
	t.Helper()
	f := &lotteryEntryFixture{lottery: ucmocks.NewMockLotteryUseCase(t), users: ucmocks.NewMockUserUseCase(t)}
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewLotteryEntryHandler(f.lottery, f.users, logger)
	srv := serveValidated(t, func(opts ...connect.HandlerOption) (string, http.Handler) {
		return lotteryentryv1connect.NewLotteryEntryServiceHandler(h, opts...)
	}, decorate)
	f.client = lotteryentryv1connect.NewLotteryEntryServiceClient(srv.Client(), srv.URL)
	return f
}

func TestLotteryEntryHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	startReq := func() *lotteryentryv1.StartRequest {
		return &lotteryentryv1.StartRequest{TicketTypeId: &entityv1.TicketTypeId{Value: tkTicketTypeID}, TicketCount: 2}
	}
	authorizeReq := func(phone string) *lotteryentryv1.AuthorizeRequest {
		return &lotteryentryv1.AuthorizeRequest{
			LotteryEntryId: &entityv1.LotteryEntryId{Value: tkEntryID},
			FullName:       &entityv1.FullName{Value: "山田太郎"},
			PhoneNumber:    &entityv1.PhoneNumber{Value: phone},
		}
	}
	draft := &entity.LotteryEntry{
		ID: tkEntryID, TicketTypeID: tkTicketTypeID, UserID: tkFanUserID, RequestedTicketCount: 2, State: entity.LotteryEntryStateDraft,
	}

	t.Run("fan starts an entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "Fan starts an entry"
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.lottery.EXPECT().Start(mock.Anything, entity.TicketTypeID(tkTicketTypeID), entity.UserID(tkFanUserID), 2).Return(draft, nil)

		resp, err := f.client.Start(ctx, connect.NewRequest(startReq()))

		require.NoError(t, err)
		assert.Equal(t, tkFanUserID, resp.Msg.GetLotteryEntry().GetUserId().GetValue(), "the Draft's user is the signed-in fan")
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_DRAFT, resp.Msg.GetLotteryEntry().GetState())
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "Not signed in"
		f := newLotteryEntryFixture(t, func(c context.Context) context.Context { return c })

		_, err := f.client.ListByTicketType(ctx, connect.NewRequest(&lotteryentryv1.ListByTicketTypeRequest{
			TicketTypeId: &entityv1.TicketTypeId{Value: tkTicketTypeID},
		}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("caller without an account", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "Caller without an account"
		f := newLotteryEntryFixture(t, signedInAs("ext-new"))
		expectNoAccount(f.users, "ext-new")

		_, err := f.client.Start(ctx, connect.NewRequest(startReq()))

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("withdraw own entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "Withdraw own entry"
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.lottery.EXPECT().WithdrawEntry(mock.Anything, entity.LotteryEntryID(tkEntryID), entity.UserID(tkFanUserID)).Return(nil)

		_, err := f.client.Withdraw(ctx, connect.NewRequest(&lotteryentryv1.WithdrawRequest{
			LotteryEntryId: &entityv1.LotteryEntryId{Value: tkEntryID},
		}))

		require.NoError(t, err)
	})

	t.Run("e.164 phone number", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "E.164 phone number"
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.lottery.EXPECT().Authorize(mock.Anything, entity.LotteryEntryID(tkEntryID), entity.UserID(tkFanUserID), "山田太郎", "+819012345678").
			Return("pi_secret", nil)

		resp, err := f.client.Authorize(ctx, connect.NewRequest(authorizeReq("+819012345678")))

		require.NoError(t, err)
		assert.Equal(t, "pi_secret", resp.Msg.GetClientSecret())
	})

	t.Run("confirm makes the entry entered", func(t *testing.T) {
		t.Parallel()
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		entered := *draft
		entered.State = entity.LotteryEntryStateEntered
		entered.Authorization = entity.PaymentAuthorization{PaymentIntentRef: "pi_1"}
		f.lottery.EXPECT().Confirm(mock.Anything, entity.LotteryEntryID(tkEntryID), entity.UserID(tkFanUserID)).Return(&entered, nil)

		resp, err := f.client.Confirm(ctx, connect.NewRequest(&lotteryentryv1.ConfirmRequest{
			LotteryEntryId: &entityv1.LotteryEntryId{Value: tkEntryID},
		}))

		require.NoError(t, err)
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_ENTERED, resp.Msg.GetLotteryEntry().GetState())
		assert.Equal(t, "pi_1", resp.Msg.GetLotteryEntry().GetAuthorization().GetPaymentIntentRef())
	})

	t.Run("list by ticket type returns the caller's entries", func(t *testing.T) {
		t.Parallel()
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		withdrawn := *draft
		withdrawn.ID, withdrawn.State = "019a0000-0000-7000-8000-0000000000d9", entity.LotteryEntryStateWithdrawn
		f.lottery.EXPECT().ListOwnByTicketType(mock.Anything, entity.TicketTypeID(tkTicketTypeID), entity.UserID(tkFanUserID)).
			Return([]*entity.LotteryEntry{draft, &withdrawn}, nil)

		resp, err := f.client.ListByTicketType(ctx, connect.NewRequest(&lotteryentryv1.ListByTicketTypeRequest{
			TicketTypeId: &entityv1.TicketTypeId{Value: tkTicketTypeID},
		}))

		require.NoError(t, err)
		require.Len(t, resp.Msg.GetLotteryEntries(), 2)
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_WITHDRAWN, resp.Msg.GetLotteryEntries()[1].GetState())
	})

	t.Run("usecase errors are returned unchanged", func(t *testing.T) {
		t.Parallel()
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.lottery.EXPECT().Start(mock.Anything, entity.TicketTypeID(tkTicketTypeID), entity.UserID(tkFanUserID), 2).
			Return(nil, apperr.New(codes.FailedPrecondition, "window closed"))

		_, err := f.client.Start(ctx, connect.NewRequest(startReq()))

		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	invalid := []struct {
		name string
		call func(c lotteryentryv1connect.LotteryEntryServiceClient) error
	}{
		{
			// @spec components/adapter/fan/api/rpc/lottery-entry "Zero tickets"
			name: "zero tickets",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				req := startReq()
				req.TicketCount = 0
				_, err := c.Start(ctx, connect.NewRequest(req))
				return err
			},
		},
		{
			// @spec components/adapter/fan/api/rpc/lottery-entry "Domestic-format phone number"
			name: "domestic-format phone number",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				_, err := c.Authorize(ctx, connect.NewRequest(authorizeReq("090-1234-5678")))
				return err
			},
		},
		{
			name: "authorize without a full name",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				req := authorizeReq("+819012345678")
				req.FullName = nil
				_, err := c.Authorize(ctx, connect.NewRequest(req))
				return err
			},
		},
		{
			name: "start without a ticket type",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				_, err := c.Start(ctx, connect.NewRequest(&lotteryentryv1.StartRequest{TicketCount: 1}))
				return err
			},
		},
		{
			name: "list without a ticket type",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				_, err := c.ListByTicketType(ctx, connect.NewRequest(&lotteryentryv1.ListByTicketTypeRequest{}))
				return err
			},
		},
		{
			name: "confirm without an entry",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				_, err := c.Confirm(ctx, connect.NewRequest(&lotteryentryv1.ConfirmRequest{}))
				return err
			},
		},
		{
			name: "withdraw with a malformed entry",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				_, err := c.Withdraw(ctx, connect.NewRequest(&lotteryentryv1.WithdrawRequest{
					LotteryEntryId: &entityv1.LotteryEntryId{Value: "not-a-uuid"},
				}))
				return err
			},
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" fails with InvalidArgument", func(t *testing.T) {
			t.Parallel()
			// No expectations: no usecase may run, so nothing is stored or held.
			f := newLotteryEntryFixture(t, signedInAs(tkFanSub))

			err := tt.call(f.client)

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}
