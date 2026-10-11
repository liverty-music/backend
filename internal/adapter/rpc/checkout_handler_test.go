package rpc_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	checkoutv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/checkout/v1/checkoutv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	checkoutv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/checkout/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// checkoutFixture serves a CheckoutHandler behind the validation interceptor.
// The usecase mocks fail the test on any call not expected.
type checkoutFixture struct {
	checkouts *ucmocks.MockCheckoutUseCase
	issuance  *ucmocks.MockIssuanceUseCase
	users     *ucmocks.MockUserUseCase
	client    checkoutv1connect.CheckoutServiceClient
}

func newCheckoutFixture(t *testing.T, decorate func(context.Context) context.Context) *checkoutFixture {
	t.Helper()
	f := &checkoutFixture{
		checkouts: ucmocks.NewMockCheckoutUseCase(t),
		issuance:  ucmocks.NewMockIssuanceUseCase(t),
		users:     ucmocks.NewMockUserUseCase(t),
	}
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewCheckoutHandler(f.checkouts, f.issuance, f.users, logger)
	srv := serveValidated(t, func(opts ...connect.HandlerOption) (string, http.Handler) {
		return checkoutv1connect.NewCheckoutServiceHandler(h, opts...)
	}, decorate)
	f.client = checkoutv1connect.NewCheckoutServiceClient(srv.Client(), srv.URL)
	return f
}

func TestCheckoutHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	held := &entity.Checkout{
		ID: tkCheckoutID, TicketTypeID: tkTicketTypeID, UserID: tkFanUserID, TicketCount: 2, Amount: 6000,
		Status: entity.CheckoutStatusHeld, HoldExpireTime: time.Date(2026, 11, 1, 1, 15, 0, 0, time.UTC),
	}
	startReq := func() *checkoutv1.StartRequest {
		return &checkoutv1.StartRequest{TicketTypeId: &entityv1.TicketTypeId{Value: tkTicketTypeID}, TicketCount: 2}
	}
	authorizeReq := func(phone string) *checkoutv1.AuthorizeCardRequest {
		return &checkoutv1.AuthorizeCardRequest{
			CheckoutId:  &entityv1.CheckoutId{Value: tkCheckoutID},
			FullName:    &entityv1.FullName{Value: "山田太郎"},
			PhoneNumber: &entityv1.PhoneNumber{Value: phone},
		}
	}

	t.Run("fan starts a checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/checkout "Fan starts a checkout"
		f := newCheckoutFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.checkouts.EXPECT().Start(mock.Anything, entity.UserID(tkFanUserID), entity.TicketTypeID(tkTicketTypeID), 2).Return(held, nil)

		resp, err := f.client.Start(ctx, connect.NewRequest(startReq()))

		require.NoError(t, err)
		assert.Equal(t, tkCheckoutID, resp.Msg.GetCheckout().GetId().GetValue())
		assert.Equal(t, entityv1.CheckoutStatus_CHECKOUT_STATUS_HELD, resp.Msg.GetCheckout().GetStatus())
		assert.Equal(t, tkTicketTypeID, resp.Msg.GetCheckout().GetTicketTypeId().GetValue())
	})

	t.Run("guest tries to buy", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/checkout "Guest tries to buy"
		f := newCheckoutFixture(t, func(c context.Context) context.Context { return c })

		_, err := f.client.Start(ctx, connect.NewRequest(startReq()))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("caller without an account", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/checkout "Caller without an account"
		f := newCheckoutFixture(t, signedInAs("ext-new"))
		expectNoAccount(f.users, "ext-new")

		_, err := f.client.Start(ctx, connect.NewRequest(startReq()))

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("fan reads their checkout", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.checkouts.EXPECT().Get(mock.Anything, entity.UserID(tkFanUserID), entity.CheckoutID(tkCheckoutID)).Return(held, nil)

		resp, err := f.client.Get(ctx, connect.NewRequest(&checkoutv1.GetRequest{CheckoutId: &entityv1.CheckoutId{Value: tkCheckoutID}}))

		require.NoError(t, err)
		assert.Equal(t, int64(6000), resp.Msg.GetCheckout().GetAmount())
	})

	t.Run("fan authorizes", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.checkouts.EXPECT().Authorize(mock.Anything, entity.UserID(tkFanUserID), entity.CheckoutID(tkCheckoutID), "山田太郎", "+819012345678").
			Return("pi_secret", nil)

		resp, err := f.client.AuthorizeCard(ctx, connect.NewRequest(authorizeReq("+819012345678")))

		require.NoError(t, err)
		assert.Equal(t, "pi_secret", resp.Msg.GetCardAuthorizationSecret().GetValue())
	})

	t.Run("fan confirms", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/checkout "Fan confirms"
		f := newCheckoutFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		caller := entity.UserID(tkFanUserID)
		f.issuance.EXPECT().IssueFromCheckout(mock.Anything, entity.CheckoutID(tkCheckoutID), &caller).Return(&entity.Order{
			ID: tkOrderID, UserID: caller, CheckoutID: tkCheckoutID, Status: entity.OrderStatusPaid, Amount: 6000, Currency: "JPY",
		}, nil)

		resp, err := f.client.Confirm(ctx, connect.NewRequest(&checkoutv1.ConfirmRequest{CheckoutId: &entityv1.CheckoutId{Value: tkCheckoutID}}))

		require.NoError(t, err)
		assert.Equal(t, tkOrderID, resp.Msg.GetOrder().GetId().GetValue())
		assert.Equal(t, tkCheckoutID, resp.Msg.GetOrder().GetCheckoutId().GetValue())
	})

	invalid := []struct {
		name string
		call func(c checkoutv1connect.CheckoutServiceClient) error
	}{
		{
			// @spec components/adapter/fan/api/rpc/checkout "Missing name"
			name: "missing name",
			call: func(c checkoutv1connect.CheckoutServiceClient) error {
				req := authorizeReq("+819012345678")
				req.FullName = nil
				_, err := c.AuthorizeCard(ctx, connect.NewRequest(req))
				return err
			},
		},
		{
			// @spec components/adapter/fan/api/rpc/checkout "Zero tickets"
			name: "zero tickets",
			call: func(c checkoutv1connect.CheckoutServiceClient) error {
				req := startReq()
				req.TicketCount = 0
				_, err := c.Start(ctx, connect.NewRequest(req))
				return err
			},
		},
		{
			// @spec components/adapter/fan/api/rpc/checkout "Domestic-format phone number"
			name: "domestic-format phone number",
			call: func(c checkoutv1connect.CheckoutServiceClient) error {
				_, err := c.AuthorizeCard(ctx, connect.NewRequest(authorizeReq("090-1234-5678")))
				return err
			},
		},
		{
			name: "start with a malformed ticket type",
			call: func(c checkoutv1connect.CheckoutServiceClient) error {
				_, err := c.Start(ctx, connect.NewRequest(&checkoutv1.StartRequest{
					TicketTypeId: &entityv1.TicketTypeId{Value: "not-a-uuid"}, TicketCount: 1,
				}))
				return err
			},
		},
		{
			name: "get without a checkout",
			call: func(c checkoutv1connect.CheckoutServiceClient) error {
				_, err := c.Get(ctx, connect.NewRequest(&checkoutv1.GetRequest{}))
				return err
			},
		},
		{
			name: "confirm without a checkout",
			call: func(c checkoutv1connect.CheckoutServiceClient) error {
				_, err := c.Confirm(ctx, connect.NewRequest(&checkoutv1.ConfirmRequest{}))
				return err
			},
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" fails with InvalidArgument", func(t *testing.T) {
			t.Parallel()
			// No expectations: no usecase may run, so nothing is held.
			f := newCheckoutFixture(t, signedInAs(tkFanSub))

			err := tt.call(f.client)

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}
