package rpc

import (
	"context"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"

	checkoutv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/checkout/v1/checkoutv1connect"
	checkoutv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/checkout/v1"
	"connectrpc.com/connect"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that CheckoutHandler satisfies the generated
// interface.
var _ checkoutv1connect.CheckoutServiceHandler = (*CheckoutHandler)(nil)

// CheckoutHandler implements the fan-facing CheckoutService: the signed-in
// fan's first-come checkout. The fan is always the caller, resolved from the
// sign-in to their stored User; a request never names a User. Requests are
// validated by the protovalidate interceptor before this handler runs.
type CheckoutHandler struct {
	checkoutUC usecase.CheckoutUseCase
	orderUC    usecase.OrderUseCase
	userUC     usecase.UserUseCase
	logger     *logging.Logger
}

// NewCheckoutHandler creates a new CheckoutHandler.
func NewCheckoutHandler(
	checkoutUC usecase.CheckoutUseCase,
	orderUC usecase.OrderUseCase,
	userUC usecase.UserUseCase,
	logger *logging.Logger,
) *CheckoutHandler {
	return &CheckoutHandler{checkoutUC: checkoutUC, orderUC: orderUC, userUC: userUC, logger: logger}
}

// Start holds tickets of a TicketType for the caller for 15 minutes.
func (h *CheckoutHandler) Start(
	ctx context.Context,
	req *connect.Request[checkoutv1.StartRequest],
) (*connect.Response[checkoutv1.StartResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	checkout, err := h.checkoutUC.Start(ctx, userID, entity.TicketTypeID(req.Msg.GetTicketTypeId().GetValue()), int(req.Msg.GetTicketCount()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&checkoutv1.StartResponse{Checkout: mapper.CheckoutToProto(checkout)}), nil
}

// Get returns the caller's Checkout as it stands now.
func (h *CheckoutHandler) Get(
	ctx context.Context,
	req *connect.Request[checkoutv1.GetRequest],
) (*connect.Response[checkoutv1.GetResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	checkout, err := h.checkoutUC.Get(ctx, userID, entity.CheckoutID(req.Msg.GetCheckoutId().GetValue()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&checkoutv1.GetResponse{Checkout: mapper.CheckoutToProto(checkout)}), nil
}

// AuthorizeCard stores the caller's full name and phone number and opens the card
// hold, returning the hold's confirmation secret.
func (h *CheckoutHandler) AuthorizeCard(
	ctx context.Context,
	req *connect.Request[checkoutv1.AuthorizeCardRequest],
) (*connect.Response[checkoutv1.AuthorizeCardResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	secret, err := h.checkoutUC.AuthorizeCard(ctx, userID, entity.CheckoutID(req.Msg.GetCheckoutId().GetValue()),
		req.Msg.GetFullName().GetValue(), req.Msg.GetPhoneNumber().GetValue())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&checkoutv1.AuthorizeCardResponse{CardAuthorizationSecret: &entityv1.CardAuthorizationSecret{Value: secret}}), nil
}

// Confirm places the Order for the caller's Checkout.
func (h *CheckoutHandler) Confirm(
	ctx context.Context,
	req *connect.Request[checkoutv1.ConfirmRequest],
) (*connect.Response[checkoutv1.ConfirmResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	order, err := h.orderUC.IssueFromCheckout(ctx, entity.CheckoutID(req.Msg.GetCheckoutId().GetValue()), &userID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&checkoutv1.ConfirmResponse{Order: mapper.OrderToProto(order)}), nil
}
