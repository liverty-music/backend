package rpc_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	orderv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/order/v1/orderv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	orderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/order/v1"
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

func newOrderClient(t *testing.T, decorate func(context.Context) context.Context) (orderv1connect.OrderServiceClient, *ucmocks.MockTicketUseCase, *ucmocks.MockUserUseCase) {
	t.Helper()
	uc := ucmocks.NewMockTicketUseCase(t)
	users := ucmocks.NewMockUserUseCase(t)
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewOrderHandler(uc, users, logger)
	srv := serveValidated(t, func(opts ...connect.HandlerOption) (string, http.Handler) {
		return orderv1connect.NewOrderServiceHandler(h, opts...)
	}, decorate)
	return orderv1connect.NewOrderServiceClient(srv.Client(), srv.URL), uc, users
}

func TestOrderHandler_Get(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	getReq := func() *orderv1.GetRequest {
		return &orderv1.GetRequest{OrderId: &entityv1.OrderId{Value: tkOrderID}}
	}

	t.Run("fan reads their order", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/order "Fan reads their order"
		client, uc, users := newOrderClient(t, signedInAs(tkFanSub))
		expectFan(users)
		uc.EXPECT().GetOrder(mock.Anything, entity.UserID(tkFanUserID), entity.OrderID(tkOrderID)).Return(&entity.Order{
			ID: tkOrderID, UserID: tkFanUserID, LotteryEntryID: tkEntryID, Status: entity.OrderStatusPaid,
			Amount: 10000, Currency: "JPY", PayTime: time.Unix(1_700_000_000, 0),
		}, nil)

		resp, err := client.Get(ctx, connect.NewRequest(getReq()))

		require.NoError(t, err)
		assert.Equal(t, tkOrderID, resp.Msg.GetOrder().GetId().GetValue())
		assert.Equal(t, tkFanUserID, resp.Msg.GetOrder().GetUserId().GetValue())
		assert.Equal(t, tkEntryID, resp.Msg.GetOrder().GetLotteryEntryId().GetValue())
		assert.Equal(t, entityv1.OrderStatus_ORDER_STATUS_PAID, resp.Msg.GetOrder().GetStatus())
	})

	t.Run("another fan's order is not found", func(t *testing.T) {
		t.Parallel()
		client, uc, users := newOrderClient(t, signedInAs(tkFanSub))
		expectFan(users)
		uc.EXPECT().GetOrder(mock.Anything, entity.UserID(tkFanUserID), entity.OrderID(tkOrderID)).
			Return(nil, apperr.New(codes.NotFound, "order not found"))

		_, err := client.Get(ctx, connect.NewRequest(getReq()))

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/order "Not signed in"
		client, _, _ := newOrderClient(t, func(c context.Context) context.Context { return c })

		_, err := client.Get(ctx, connect.NewRequest(getReq()))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("caller without an account", func(t *testing.T) {
		t.Parallel()
		client, _, users := newOrderClient(t, signedInAs("ext-new"))
		expectNoAccount(users, "ext-new")

		_, err := client.Get(ctx, connect.NewRequest(getReq()))

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("missing order", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/order "Missing order"
		client, _, _ := newOrderClient(t, signedInAs(tkFanSub))

		_, err := client.Get(ctx, connect.NewRequest(&orderv1.GetRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
