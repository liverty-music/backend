package rpc

import (
	"context"
	"errors"
	"time"

	adminorderv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/order/v1/orderv1connect"
	adminorderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/order/v1"
	"connectrpc.com/connect/v2"
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
	orderUC usecase.OrderUseCase
	logger  *logging.Logger
}

// NewAdminOrderHandler creates a new AdminOrderHandler.
func NewAdminOrderHandler(
	orderUC usecase.OrderUseCase,
	logger *logging.Logger,
) *AdminOrderHandler {
	return &AdminOrderHandler{
		orderUC: orderUC,
		logger:  logger,
	}
}

// Get implements [adminorderv1connect.OrderServiceHandler]. It returns the
// Order with its Settlement and the Settlement's splits, or the Order alone
// when it has no Settlement. The request is validated by the protovalidate
// interceptor before this runs; errors from the use case are returned
// unchanged (NotFound for an unknown Order).
func (h *AdminOrderHandler) Get(
	ctx context.Context,
	req *adminorderv1.GetRequest,
) (*adminorderv1.GetResponse, error) {
	order, settlement, err := h.orderUC.GetWithSettlement(ctx, entity.OrderID(req.GetOrderId().GetValue()))
	if err != nil {
		return nil, err
	}
	return &adminorderv1.GetResponse{
		Order:      mapper.OrderToProto(order),
		Settlement: mapper.SettlementToProto(settlement),
	}, nil
}

// ListByEvent implements [adminorderv1connect.OrderServiceHandler]. It returns
// every Order of the Event, in any status. The request is validated by the
// protovalidate interceptor before this runs.
func (h *AdminOrderHandler) ListByEvent(
	ctx context.Context,
	req *adminorderv1.ListByEventRequest,
) (*adminorderv1.ListByEventResponse, error) {
	orders, err := h.orderUC.ListByEvent(ctx, req.GetEventId().GetValue())
	if err != nil {
		return nil, err
	}
	resp := &adminorderv1.ListByEventResponse{}
	for _, o := range orders {
		resp.Orders = append(resp.Orders, mapper.OrderToProto(o))
	}
	return resp, nil
}

// Refund implements [adminorderv1connect.OrderServiceHandler].
//
// It maps the proto RefundReason to the domain RefundReason and delegates to
// OrderUseCase.Refund. The handler applies no business logic; error mapping from
// apperr to Connect codes is the sole responsibility here.
func (h *AdminOrderHandler) Refund(
	ctx context.Context,
	req *adminorderv1.RefundRequest,
) (*adminorderv1.RefundResponse, error) {
	if req.GetOrderId() == nil || req.GetOrderId().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "order_id is required")
	}

	protoReason := req.GetReason()
	if protoReason == adminorderv1.RefundReason_REFUND_REASON_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, "reason must be specified")
	}

	orderID := entity.OrderID(req.GetOrderId().GetValue())
	reason := mapper.ProtoRefundReasonToDomain(protoReason)

	order, err := h.orderUC.Refund(ctx, orderID, reason, time.Now())
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "order not found")
		}
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
		}
		if errors.Is(err, apperr.ErrInvalidArgument) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		return nil, err
	}

	return &adminorderv1.RefundResponse{
		Order: mapper.OrderToProto(order),
	}, nil
}
