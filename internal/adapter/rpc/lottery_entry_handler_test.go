package rpc_test

import (
	"context"
	"strings"
	"testing"

	lotteryentryv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/lottery_entry/v1/lottery_entryv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	lotteryentryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/lottery_entry/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
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
	lottery *ucmocks.MockLotteryEntryUseCase
	users   *ucmocks.MockUserUseCase
	client  lotteryentryv1connect.LotteryEntryServiceClient
}

func newLotteryEntryFixture(t *testing.T, decorate func(context.Context) context.Context) *lotteryEntryFixture {
	t.Helper()
	f := &lotteryEntryFixture{lottery: ucmocks.NewMockLotteryEntryUseCase(t), users: ucmocks.NewMockUserUseCase(t)}
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewLotteryEntryHandler(f.lottery, f.users, logger)
	srv := serveValidated(t, func(s *connect.Server) {
		lotteryentryv1connect.RegisterLotteryEntryServiceHandler(s, h)
	}, decorate)
	f.client = lotteryentryv1connect.NewLotteryEntryServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	return f
}

func TestLotteryEntryHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const cardAuthorizationID = "pi_1"
	authorizeReq := func(phone string) *lotteryentryv1.AuthorizeCardRequest {
		return &lotteryentryv1.AuthorizeCardRequest{
			TicketSaleId:  &entityv1.TicketSaleId{Value: tkSaleID},
			TicketStockId: &entityv1.TicketStockId{Value: tkStockID},
			TicketCount:   2,
			FullName:      &entityv1.FullName{Value: "山田太郎"},
			PhoneNumber:   &entityv1.PhoneNumber{Value: phone},
		}
	}
	expectAuthorize := func(f *lotteryEntryFixture, code *entity.ReferralCode) {
		f.lottery.EXPECT().AuthorizeCard(mock.Anything, entity.TicketSaleID(tkSaleID), entity.TicketStockID(tkStockID), entity.UserID(tkFanUserID), 2,
			"山田太郎", "+819012345678", code).
			Return(&entity.CardAuthorization{ID: cardAuthorizationID, Secret: "pi_1_secret"}, nil)
	}
	entered := &entity.LotteryEntry{
		ID: tkEntryID, OrderID: tkOrderID, TicketSaleID: tkSaleID, State: entity.LotteryEntryStateEntered,
		Order: &entity.Order{
			ID: tkOrderID, UserID: tkFanUserID, TicketStockID: tkStockID, TicketCount: 2,
			CardAuthorizationID: cardAuthorizationID, LotteryEntryID: tkEntryID,
			Status: entity.OrderStatusPending, Amount: 6000, Currency: "JPY",
		},
	}

	t.Run("fan authorizes their card", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "Fan authorizes their card"
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		expectAuthorize(f, nil)

		resp, err := f.client.AuthorizeCard(ctx, authorizeReq("+819012345678"))

		require.NoError(t, err)
		assert.Equal(t, cardAuthorizationID, resp.GetCardAuthorizationId().GetValue())
		assert.Equal(t, "pi_1_secret", resp.GetCardAuthorizationSecret().GetValue())
	})

	t.Run("e.164 phone number", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "E.164 phone number"
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		expectAuthorize(f, nil)

		_, err := f.client.AuthorizeCard(ctx, authorizeReq("+819012345678"))

		require.NoError(t, err)
	})

	t.Run("referral code passed on", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "Referral code passed on"
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		code := entity.ReferralCode("member-a")
		expectAuthorize(f, &code)
		req := authorizeReq("+819012345678")
		req.ReferralCode = &entityv1.ReferralCode{Value: "member-a"}

		_, err := f.client.AuthorizeCard(ctx, req)

		require.NoError(t, err)
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "Not signed in"
		f := newLotteryEntryFixture(t, func(c context.Context) context.Context { return c })

		_, err := f.client.ListByTicketSale(ctx, &lotteryentryv1.ListByTicketSaleRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID},
		})

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("caller without an account", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "Caller without an account"
		f := newLotteryEntryFixture(t, signedInAs("ext-new"))
		expectNoAccount(f.users, "ext-new")

		_, err := f.client.AuthorizeCard(ctx, authorizeReq("+819012345678"))

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("fan confirms", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/lottery-entry "Fan confirms"
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		fan := entity.UserID(tkFanUserID)
		f.lottery.EXPECT().Confirm(mock.Anything, cardAuthorizationID, &fan).Return(entered, nil)

		resp, err := f.client.Confirm(ctx, &lotteryentryv1.ConfirmRequest{
			CardAuthorizationId: &entityv1.CardAuthorizationId{Value: cardAuthorizationID},
		})

		require.NoError(t, err)
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_ENTERED, resp.GetLotteryEntry().GetState())
		assert.Equal(t, tkOrderID, resp.GetLotteryEntry().GetOrderId().GetValue())
		assert.Equal(t, tkSaleID, resp.GetLotteryEntry().GetTicketSaleId().GetValue())
		// The entry's Order carries the fan, stock, count and card authorization.
		assert.Equal(t, tkFanUserID, resp.GetOrder().GetUserId().GetValue())
		assert.Equal(t, tkStockID, resp.GetOrder().GetTicketStockId().GetValue())
		assert.Equal(t, int32(2), resp.GetOrder().GetTicketCount())
		assert.Equal(t, cardAuthorizationID, resp.GetOrder().GetCardAuthorizationId().GetValue())
	})

	t.Run("list by ticket sale returns the caller's entries", func(t *testing.T) {
		t.Parallel()
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.lottery.EXPECT().ListByTicketSale(mock.Anything, entity.TicketSaleID(tkSaleID), entity.UserID(tkFanUserID)).
			Return([]*entity.LotteryEntry{entered}, nil)

		resp, err := f.client.ListByTicketSale(ctx, &lotteryentryv1.ListByTicketSaleRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID},
		})

		require.NoError(t, err)
		require.Len(t, resp.GetLotteryEntries(), 1)
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_ENTERED, resp.GetLotteryEntries()[0].GetState())
		require.Len(t, resp.GetOrders(), 1)
		assert.Equal(t, tkOrderID, resp.GetOrders()[0].GetId().GetValue())
	})

	t.Run("usecase errors are returned unchanged", func(t *testing.T) {
		t.Parallel()
		f := newLotteryEntryFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.lottery.EXPECT().AuthorizeCard(mock.Anything, entity.TicketSaleID(tkSaleID), entity.TicketStockID(tkStockID), entity.UserID(tkFanUserID), 2,
			"山田太郎", "+819012345678", (*entity.ReferralCode)(nil)).
			Return(nil, apperr.New(codes.FailedPrecondition, "window closed"))

		_, err := f.client.AuthorizeCard(ctx, authorizeReq("+819012345678"))

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
				req := authorizeReq("+819012345678")
				req.TicketCount = 0
				_, err := c.AuthorizeCard(ctx, req)
				return err
			},
		},
		{
			// @spec components/adapter/fan/api/rpc/lottery-entry "Malformed referral code"
			name: "malformed referral code",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				req := authorizeReq("+819012345678")
				req.ReferralCode = &entityv1.ReferralCode{Value: strings.Repeat("a", 33)}
				_, err := c.AuthorizeCard(ctx, req)
				return err
			},
		},
		{
			// @spec components/adapter/fan/api/rpc/lottery-entry "Domestic-format phone number"
			name: "domestic-format phone number",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				_, err := c.AuthorizeCard(ctx, authorizeReq("090-1234-5678"))
				return err
			},
		},
		{
			name: "authorize without a full name",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				req := authorizeReq("+819012345678")
				req.FullName = nil
				_, err := c.AuthorizeCard(ctx, req)
				return err
			},
		},
		{
			// @spec components/adapter/fan/api/rpc/lottery-entry "No stock"
			name: "authorize without a ticket stock",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				req := authorizeReq("+819012345678")
				req.TicketStockId = nil
				_, err := c.AuthorizeCard(ctx, req)
				return err
			},
		},
		{
			name: "authorize without a ticket sale",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				req := authorizeReq("+819012345678")
				req.TicketSaleId = nil
				_, err := c.AuthorizeCard(ctx, req)
				return err
			},
		},
		{
			name: "list without a ticket sale",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				_, err := c.ListByTicketSale(ctx, &lotteryentryv1.ListByTicketSaleRequest{})
				return err
			},
		},
		{
			name: "confirm without a card authorization",
			call: func(c lotteryentryv1connect.LotteryEntryServiceClient) error {
				_, err := c.Confirm(ctx, &lotteryentryv1.ConfirmRequest{})
				return err
			},
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" fails with InvalidArgument", func(t *testing.T) {
			t.Parallel()
			// No expectations: no usecase may run, so no card authorization is
			// opened and no entry is made.
			f := newLotteryEntryFixture(t, signedInAs(tkFanSub))

			err := tt.call(f.client)

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}
