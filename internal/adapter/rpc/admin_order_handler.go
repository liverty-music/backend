package rpc

import (
	"context"
	"errors"
	"time"

	adminorderv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/order/v1/orderv1connect"
	adminorderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/order/v1"
	"connectrpc.com/connect"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that AdminOrderHandler satisfies the generated interface.
var _ adminorderv1connect.OrderServiceHandler = (*AdminOrderHandler)(nil)

// AdminOrderHandler implements the admin-facing OrderService Connect
// interface. Server-wide RequireRoleInterceptor(admin) gates every method
// before this handler runs; the handler only maps Proto↔Entity and delegates
// business logic to the use case.
type AdminOrderHandler struct {
	refundUC usecase.RefundOrderUseCase
	logger   *logging.Logger
}

// NewAdminOrderHandler creates a new AdminOrderHandler.
func NewAdminOrderHandler(
	refundUC usecase.RefundOrderUseCase,
	logger *logging.Logger,
) *AdminOrderHandler {
	return &AdminOrderHandler{
		refundUC: refundUC,
		logger:   logger,
	}
}

// Refund implements [adminorderv1connect.OrderServiceHandler].
//
// It maps the proto RefundReason to the domain RefundReason and delegates to
// RefundOrderUseCase. The handler applies no business logic; error mapping from
// apperr to Connect codes is the sole responsibility here.
func (h *AdminOrderHandler) Refund(
	ctx context.Context,
	req *connect.Request[adminorderv1.RefundRequest],
) (*connect.Response[adminorderv1.RefundResponse], error) {
	if req.Msg.GetOrderId() == nil || req.Msg.GetOrderId().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("order_id is required"))
	}

	protoReason := req.Msg.GetReason()
	if protoReason == adminorderv1.RefundReason_REFUND_REASON_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("reason must be specified"))
	}

	orderID := entity.OrderID(req.Msg.GetOrderId().GetValue())
	reason := mapper.ProtoRefundReasonToDomain(protoReason)

	order, err := h.refundUC.RefundOrder(ctx, orderID, reason, time.Now())
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("order not found"))
		}
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		if errors.Is(err, apperr.ErrInvalidArgument) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, err
	}

	return connect.NewResponse(&adminorderv1.RefundResponse{
		Order: mapper.OrderToProto(order),
	}), nil
}
