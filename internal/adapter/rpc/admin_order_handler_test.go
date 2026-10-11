package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminorderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/order/v1"
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

func newAdminOrderHandler(t *testing.T, orderUC usecase.OrderUseCase, refundUC usecase.RefundOrderUseCase) *handler.AdminOrderHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewAdminOrderHandler(orderUC, refundUC, logger)
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
		resp, err := newAdminOrderHandler(t, ucmocks.NewMockOrderUseCase(t), uc).Refund(context.Background(), connect.NewRequest(&adminorderv1.RefundRequest{
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
		_, err := newAdminOrderHandler(t, ucmocks.NewMockOrderUseCase(t), uc).Refund(context.Background(), connect.NewRequest(&adminorderv1.RefundRequest{
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
		_, err := newAdminOrderHandler(t, ucmocks.NewMockOrderUseCase(t), uc).Refund(context.Background(), connect.NewRequest(&adminorderv1.RefundRequest{
			OrderId: &entityv1.OrderId{Value: "order-x"},
			Reason:  adminorderv1.RefundReason_REFUND_REASON_CANCELLATION,
		}))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}

func TestAdminOrderHandler_Get(t *testing.T) {
	t.Parallel()
	const orderID = "019a0000-0000-7000-8000-0000000000f1"
	order := &entity.Order{ID: orderID, UserID: tkFanUserID, CheckoutID: tkCheckoutID, Status: entity.OrderStatusPaid, Amount: 6000, Currency: "JPY"}

	t.Run("returns the order with its settlement and splits", func(t *testing.T) {
		t.Parallel()
		orders := ucmocks.NewMockOrderUseCase(t)
		orders.EXPECT().GetWithSettlement(mock.Anything, entity.OrderID(orderID)).Return(order, &entity.Settlement{
			ID: "019a0000-0000-7000-8000-0000000000f2", OrderID: orderID, Status: entity.SettlementStatusHeld,
			Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 5520}},
		}, nil)

		resp, err := newAdminOrderHandler(t, orders, &refundUCStub{}).Get(context.Background(),
			connect.NewRequest(&adminorderv1.GetRequest{OrderId: &entityv1.OrderId{Value: orderID}}))

		require.NoError(t, err)
		assert.Equal(t, orderID, resp.Msg.GetOrder().GetId().GetValue())
		assert.Equal(t, entityv1.SettlementStatus_SETTLEMENT_STATUS_HELD, resp.Msg.GetSettlement().GetStatus())
		require.Len(t, resp.Msg.GetSettlement().GetSplits(), 1)
		assert.Equal(t, int64(5520), resp.Msg.GetSettlement().GetSplits()[0].GetAmount())
	})

	t.Run("returns the order alone without a settlement", func(t *testing.T) {
		t.Parallel()
		orders := ucmocks.NewMockOrderUseCase(t)
		orders.EXPECT().GetWithSettlement(mock.Anything, entity.OrderID(orderID)).Return(order, nil, nil)

		resp, err := newAdminOrderHandler(t, orders, &refundUCStub{}).Get(context.Background(),
			connect.NewRequest(&adminorderv1.GetRequest{OrderId: &entityv1.OrderId{Value: orderID}}))

		require.NoError(t, err)
		assert.Equal(t, orderID, resp.Msg.GetOrder().GetId().GetValue())
		assert.Nil(t, resp.Msg.GetSettlement())
	})
}
