package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminorderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/order/v1"
	"connectrpc.com/connect/v2"
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

// refundUCStub is a function-field OrderRefunder that records its call.
type refundUCStub struct {
	called bool
	reason usecase.RefundReason
	fn     func(entity.OrderID) (*entity.Order, error)
}

func (s *refundUCStub) Refund(_ context.Context, orderID entity.OrderID, reason usecase.RefundReason) (*entity.Order, error) {
	s.called, s.reason = true, reason
	return s.fn(orderID)
}

func newAdminOrderHandler(t *testing.T, orderUC usecase.OrderUseCase, refundUC usecase.OrderRefunder) *handler.AdminOrderHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewAdminOrderHandler(orderWithRefund{OrderUseCase: orderUC, refunder: refundUC}, logger)
}

// orderWithRefund serves the admin OrderService from orderUC, with Refund
// answered by refunder.
type orderWithRefund struct {
	usecase.OrderUseCase
	refunder usecase.OrderRefunder
}

func (o orderWithRefund) Refund(ctx context.Context, orderID entity.OrderID, reason usecase.RefundReason) (*entity.Order, error) {
	return o.refunder.Refund(ctx, orderID, reason)
}

const refundTestOrderID = "019a0000-0000-7000-8000-0000000000b1"

func TestAdminOrderHandler_Refund(t *testing.T) {
	t.Parallel()

	t.Run("admin refunds a cancelled event's order", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Admin refunds a cancelled event's order"
		uc := &refundUCStub{fn: func(id entity.OrderID) (*entity.Order, error) {
			return &entity.Order{ID: id, Status: entity.OrderStatusRefunded, Currency: "JPY"}, nil
		}}
		h := newAdminOrderHandler(t, ucmocks.NewMockOrderUseCase(t), uc)
		resp, err := callThroughAdminPipeline(t, adminCtx(), &adminorderv1.RefundRequest{
			OrderId: &entityv1.OrderId{Value: refundTestOrderID},
			Reason:  adminorderv1.RefundReason_REFUND_REASON_CANCELLATION,
		}, h.Refund)
		require.NoError(t, err)
		assert.Equal(t, usecase.RefundReasonCancellation, uc.reason)
		assert.Equal(t, refundTestOrderID, resp.Order.Id.Value)
		assert.Equal(t, entityv1.OrderStatus_ORDER_STATUS_REFUNDED, resp.Order.Status)
	})

	t.Run("reason missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Reason missing"
		uc := &refundUCStub{}
		h := newAdminOrderHandler(t, ucmocks.NewMockOrderUseCase(t), uc)
		_, err := callThroughAdminPipeline(t, adminCtx(), &adminorderv1.RefundRequest{
			OrderId: &entityv1.OrderId{Value: refundTestOrderID},
		}, h.Refund)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		assert.False(t, uc.called, "nothing is refunded")
	})

	t.Run("unknown order", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Unknown order"
		uc := &refundUCStub{fn: func(entity.OrderID) (*entity.Order, error) {
			return nil, apperr.New(codes.NotFound, "order not found")
		}}
		h := newAdminOrderHandler(t, ucmocks.NewMockOrderUseCase(t), uc)
		_, err := callThroughAdminPipeline(t, adminCtx(), &adminorderv1.RefundRequest{
			OrderId: &entityv1.OrderId{Value: refundTestOrderID},
			Reason:  adminorderv1.RefundReason_REFUND_REASON_CANCELLATION,
		}, h.Refund)
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
			&adminorderv1.GetRequest{OrderId: &entityv1.OrderId{Value: orderID}})

		require.NoError(t, err)
		assert.Equal(t, orderID, resp.GetOrder().GetId().GetValue())
		assert.Equal(t, entityv1.SettlementStatus_SETTLEMENT_STATUS_HELD, resp.GetSettlement().GetStatus())
		require.Len(t, resp.GetSettlement().GetSplits(), 1)
		assert.Equal(t, int64(5520), resp.GetSettlement().GetSplits()[0].GetAmount())
	})

	t.Run("returns the order alone without a settlement", func(t *testing.T) {
		t.Parallel()
		orders := ucmocks.NewMockOrderUseCase(t)
		orders.EXPECT().GetWithSettlement(mock.Anything, entity.OrderID(orderID)).Return(order, nil, nil)

		resp, err := newAdminOrderHandler(t, orders, &refundUCStub{}).Get(context.Background(),
			&adminorderv1.GetRequest{OrderId: &entityv1.OrderId{Value: orderID}})

		require.NoError(t, err)
		assert.Equal(t, orderID, resp.GetOrder().GetId().GetValue())
		assert.Nil(t, resp.GetSettlement())
	})
}
