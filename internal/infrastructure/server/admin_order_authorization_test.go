package server_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	adminorderv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/order/v1/orderv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminorderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/order/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/liverty-music/backend/internal/usecase"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	adminOrderToken   = "order-admin-token"
	adminViewerToken  = "viewer-token"
	adminOrderOrderID = "019a0000-0000-7000-8000-0000000000f1"
)

// unreachableRefundUC fails the test if a refund runs.
type unreachableRefundUC struct{ t *testing.T }

func (u unreachableRefundUC) Refund(context.Context, entity.OrderID, usecase.RefundReason, time.Time) (*entity.Order, error) {
	u.t.Error("Refund must not run")
	return nil, nil
}

// orderWithRefund serves the admin OrderService from orderUC, with Refund
// answered by refunder.
type orderWithRefund struct {
	usecase.OrderUseCase
	refunder usecase.OrderRefunder
}

func (o orderWithRefund) Refund(ctx context.Context, orderID entity.OrderID, reason usecase.RefundReason, now time.Time) (*entity.Order, error) {
	return o.refunder.Refund(ctx, orderID, reason, now)
}

// newTestAdminOrderServer builds an admin Connect server, with the admin role
// gate as provider.go wires it, serving the admin OrderService.
func newTestAdminOrderServer(t *testing.T, orderUC usecase.OrderUseCase, refundUC usecase.OrderRefunder) *httptest.Server {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	validator := authmocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateToken(mock.Anything, adminOrderToken).
		Return(&auth.Claims{Sub: "admin-user", Roles: []string{"admin"}}, nil).Maybe()
	validator.EXPECT().ValidateToken(mock.Anything, adminViewerToken).
		Return(&auth.Claims{Sub: "regular-user", Roles: []string{"viewer"}}, nil).Maybe()

	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{AuthRPS: 100, AuthBurst: 100, AnonRPS: 100, AnonBurst: 100}, time.Minute)
	t.Cleanup(func() { _ = rateLimiter.Close() })
	healthHandler := func(*connect.Server) {}
	handlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			adminorderv1connect.RegisterOrderServiceHandler(s, rpc.NewAdminOrderHandler(orderWithRefund{OrderUseCase: orderUC, refunder: refundUC}, logger))
		},
	}
	cfg := config.ServerSettings{
		Host: "127.0.0.1", HandlerTimeout: 5 * time.Second, ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second,
	}
	srv := server.NewConnectServer(cfg, logger, auth.NewAuthFunc(validator, nil), rateLimiter, healthHandler,
		[]connect.ServerInterceptor{auth.NewRequireRoleInterceptor("admin")}, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestAdminServer_OrderRefundAuthorization(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/admin/api/rpc/order "Non-admin"
	ts := newTestAdminOrderServer(t, usecasemocks.NewMockOrderUseCase(t), unreachableRefundUC{t: t})

	req := &adminorderv1.RefundRequest{
		OrderId: &entityv1.OrderId{Value: adminOrderOrderID},
		Reason:  adminorderv1.RefundReason_REFUND_REASON_CANCELLATION,
	}
	_, err := adminorderv1connect.NewOrderServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL))).Refund(withBearer(context.Background(), adminViewerToken), req)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestAdminServer_OrderGet(t *testing.T) {
	t.Parallel()

	paid := &entity.Order{
		ID: adminOrderOrderID, UserID: "019a0000-0000-7000-8000-0000000000f3", CheckoutID: "019a0000-0000-7000-8000-0000000000f4",
		Status: entity.OrderStatusPaid, Amount: 6000, Currency: "JPY",
	}
	get := func(t *testing.T, ts *httptest.Server, token string, orderID *entityv1.OrderId) (*adminorderv1.GetResponse, error) {
		t.Helper()
		req := &adminorderv1.GetRequest{OrderId: orderID}
		return adminorderv1connect.NewOrderServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL))).Get(withBearer(context.Background(), token), req)
	}

	t.Run("paid order of a first-party concert", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Paid order of a first-party concert"
		orders := usecasemocks.NewMockOrderUseCase(t)
		orders.EXPECT().GetWithSettlement(mock.Anything, entity.OrderID(adminOrderOrderID)).Return(paid, &entity.Settlement{
			ID: "019a0000-0000-7000-8000-0000000000f2", OrderID: adminOrderOrderID, ProviderChargeID: "ch_1", Status: entity.SettlementStatusHeld,
			Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 5520}},
		}, nil)
		ts := newTestAdminOrderServer(t, orders, unreachableRefundUC{t: t})

		resp, err := get(t, ts, adminOrderToken, &entityv1.OrderId{Value: adminOrderOrderID})

		require.NoError(t, err)
		assert.Equal(t, entityv1.OrderStatus_ORDER_STATUS_PAID, resp.GetOrder().GetStatus())
		assert.Equal(t, entityv1.SettlementStatus_SETTLEMENT_STATUS_HELD, resp.GetSettlement().GetStatus())
		require.Len(t, resp.GetSettlement().GetSplits(), 1)
		assert.Equal(t, "org-1", resp.GetSettlement().GetSplits()[0].GetPayeeOrganizerId().GetValue())
	})

	t.Run("order without a settlement", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Order without a settlement"
		orders := usecasemocks.NewMockOrderUseCase(t)
		orders.EXPECT().GetWithSettlement(mock.Anything, entity.OrderID(adminOrderOrderID)).Return(paid, nil, nil)
		ts := newTestAdminOrderServer(t, orders, unreachableRefundUC{t: t})

		resp, err := get(t, ts, adminOrderToken, &entityv1.OrderId{Value: adminOrderOrderID})

		require.NoError(t, err)
		assert.Equal(t, adminOrderOrderID, resp.GetOrder().GetId().GetValue())
		assert.Nil(t, resp.GetSettlement())
	})

	t.Run("unknown order", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Unknown order"
		orders := usecasemocks.NewMockOrderUseCase(t)
		orders.EXPECT().GetWithSettlement(mock.Anything, entity.OrderID(adminOrderOrderID)).
			Return(nil, nil, apperr.New(codes.NotFound, "order not found"))
		ts := newTestAdminOrderServer(t, orders, unreachableRefundUC{t: t})

		_, err := get(t, ts, adminOrderToken, &entityv1.OrderId{Value: adminOrderOrderID})

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("non-admin reads an order", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Non-admin reads an order"
		ts := newTestAdminOrderServer(t, usecasemocks.NewMockOrderUseCase(t), unreachableRefundUC{t: t})

		resp, err := get(t, ts, adminViewerToken, &entityv1.OrderId{Value: adminOrderOrderID})

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.Nil(t, resp)
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		ts := newTestAdminOrderServer(t, usecasemocks.NewMockOrderUseCase(t), unreachableRefundUC{t: t})

		_, err := get(t, ts, "", &entityv1.OrderId{Value: adminOrderOrderID})

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("missing order", func(t *testing.T) {
		t.Parallel()
		ts := newTestAdminOrderServer(t, usecasemocks.NewMockOrderUseCase(t), unreachableRefundUC{t: t})

		_, err := get(t, ts, adminOrderToken, nil)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestAdminServer_OrderListByEvent(t *testing.T) {
	t.Parallel()

	const eventID = "019a0000-0000-7000-8000-0000000000e1"
	listByEvent := func(t *testing.T, ts *httptest.Server, token string, id *entityv1.EventId) (*adminorderv1.ListByEventResponse, error) {
		t.Helper()
		req := &adminorderv1.ListByEventRequest{EventId: id}
		return adminorderv1connect.NewOrderServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL))).ListByEvent(withBearer(context.Background(), token), req)
	}

	t.Run("admin lists an event's orders", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Admin lists an event's orders"
		orders := usecasemocks.NewMockOrderUseCase(t)
		orders.EXPECT().ListByEvent(mock.Anything, eventID).Return([]*entity.Order{{
			ID: adminOrderOrderID, UserID: "019a0000-0000-7000-8000-0000000000f3", CheckoutID: "019a0000-0000-7000-8000-0000000000f4",
			Status: entity.OrderStatusPaid, Amount: 6000, Currency: "JPY", PayTime: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		}}, nil)
		ts := newTestAdminOrderServer(t, orders, unreachableRefundUC{t: t})

		resp, err := listByEvent(t, ts, adminOrderToken, &entityv1.EventId{Value: eventID})

		require.NoError(t, err)
		require.Len(t, resp.GetOrders(), 1)
		assert.Equal(t, entityv1.OrderStatus_ORDER_STATUS_PAID, resp.GetOrders()[0].GetStatus())
		assert.Equal(t, adminOrderOrderID, resp.GetOrders()[0].GetId().GetValue())
	})

	t.Run("event missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Event missing"
		ts := newTestAdminOrderServer(t, usecasemocks.NewMockOrderUseCase(t), unreachableRefundUC{t: t})

		_, err := listByEvent(t, ts, adminOrderToken, nil)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("non-admin lists orders", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/order "Non-admin lists orders"
		ts := newTestAdminOrderServer(t, usecasemocks.NewMockOrderUseCase(t), unreachableRefundUC{t: t})

		resp, err := listByEvent(t, ts, adminViewerToken, &entityv1.EventId{Value: eventID})

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.Nil(t, resp)
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		ts := newTestAdminOrderServer(t, usecasemocks.NewMockOrderUseCase(t), unreachableRefundUC{t: t})

		_, err := listByEvent(t, ts, "", &entityv1.EventId{Value: eventID})

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}
