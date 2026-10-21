package rpc

import (
	"context"

	orderv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/order/v1/orderv1connect"
	orderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/order/v1"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that OrderHandler satisfies the generated interface.
var _ orderv1connect.OrderServiceHandler = (*OrderHandler)(nil)

// OrderHandler implements the fan-facing OrderService: a signed-in fan reads
// their own Orders. The Order's user is always the caller; a request never
// names a User.
type OrderHandler struct {
	orderUC usecase.OrderUseCase
	userUC  usecase.UserUseCase
	logger  *logging.Logger
}

// NewOrderHandler creates a new OrderHandler.
func NewOrderHandler(orderUC usecase.OrderUseCase, userUC usecase.UserUseCase, logger *logging.Logger) *OrderHandler {
	return &OrderHandler{orderUC: orderUC, userUC: userUC, logger: logger}
}

// Get returns one of the caller's own Orders.
func (h *OrderHandler) Get(
	ctx context.Context,
	req *orderv1.GetRequest,
) (*orderv1.GetResponse, error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	order, err := h.orderUC.Get(ctx, userID, entity.OrderID(req.GetOrderId().GetValue()))
	if err != nil {
		return nil, err
	}
	return &orderv1.GetResponse{Order: mapper.OrderToProto(order)}, nil
}
