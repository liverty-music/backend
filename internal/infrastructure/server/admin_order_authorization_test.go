package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminorderv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/order/v1/orderv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminorderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/order/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// unreachableRefundUC fails the test if a refund runs.
type unreachableRefundUC struct{ t *testing.T }

func (u unreachableRefundUC) RefundOrder(context.Context, entity.OrderID, usecase.RefundReason, time.Time) (*entity.Order, error) {
	u.t.Error("RefundOrder must not run")
	return nil, nil
}

func TestAdminServer_OrderRefundAuthorization(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/admin/api/rpc/order "Non-admin"
	logger, err := logging.New()
	require.NoError(t, err)
	validator := authmocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateToken(mock.Anything, "viewer-token").
		Return(&auth.Claims{Sub: "regular-user", Roles: []string{"viewer"}}, nil)

	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{AuthRPS: 100, AuthBurst: 100, AnonRPS: 100, AnonBurst: 100}, time.Minute)
	t.Cleanup(func() { _ = rateLimiter.Close() })
	healthHandler := func(_ ...connect.HandlerOption) (string, http.Handler) {
		return "/unused-health-check/", http.NotFoundHandler()
	}
	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return adminorderv1connect.NewOrderServiceHandler(rpc.NewAdminOrderHandler(unreachableRefundUC{t: t}, logger), opts...)
		},
	}
	cfg := config.ServerSettings{
		Host: "127.0.0.1", HandlerTimeout: 5 * time.Second, ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second,
	}
	srv := server.NewConnectServer(cfg, logger, auth.NewAuthFunc(validator, nil), rateLimiter, healthHandler,
		[]connect.Interceptor{auth.NewRequireRoleInterceptor("admin")}, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	req := connect.NewRequest(&adminorderv1.RefundRequest{
		OrderId: &entityv1.OrderId{Value: "019a0000-0000-7000-8000-0000000000f1"},
		Reason:  adminorderv1.RefundReason_REFUND_REASON_CANCELLATION,
	})
	req.Header().Set("Authorization", "Bearer viewer-token")
	_, err = adminorderv1connect.NewOrderServiceClient(ts.Client(), ts.URL).Refund(context.Background(), req)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}
