package rpc

import (
	"context"

	resalelistingv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/resale_listing/v1/resale_listingv1connect"
	resalelistingv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/resale_listing/v1"
	"connectrpc.com/connect"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that ResaleListingHandler satisfies the generated
// interface.
var _ resalelistingv1connect.ResaleListingServiceHandler = (*ResaleListingHandler)(nil)

// ResaleListingHandler implements the fan-facing ResaleListingService: the
// signed-in fan's official resale listings. The fan is always the caller,
// resolved from the sign-in to their stored User; a request never names a
// User. Requests are validated by the protovalidate interceptor before this
// handler runs.
type ResaleListingHandler struct {
	resaleListingUC usecase.ResaleListingUseCase
	userUC          usecase.UserUseCase
	logger          *logging.Logger
}

// NewResaleListingHandler creates a new ResaleListingHandler.
func NewResaleListingHandler(resaleListingUC usecase.ResaleListingUseCase, userUC usecase.UserUseCase, logger *logging.Logger) *ResaleListingHandler {
	return &ResaleListingHandler{resaleListingUC: resaleListingUC, userUC: userUC, logger: logger}
}

// Create lists one of the caller's tickets for official resale.
func (h *ResaleListingHandler) Create(
	ctx context.Context,
	req *connect.Request[resalelistingv1.CreateRequest],
) (*connect.Response[resalelistingv1.CreateResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	listing, err := h.resaleListingUC.Create(ctx, userID, entity.TicketID(req.Msg.GetTicketId().GetValue()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&resalelistingv1.CreateResponse{ResaleListing: mapper.ResaleListingToProto(listing)}), nil
}

// Withdraw takes back one of the caller's listings.
func (h *ResaleListingHandler) Withdraw(
	ctx context.Context,
	req *connect.Request[resalelistingv1.WithdrawRequest],
) (*connect.Response[resalelistingv1.WithdrawResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	listing, err := h.resaleListingUC.Withdraw(ctx, userID, entity.ResaleListingID(req.Msg.GetResaleListingId().GetValue()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&resalelistingv1.WithdrawResponse{ResaleListing: mapper.ResaleListingToProto(listing)}), nil
}

// List returns all of the caller's listings.
func (h *ResaleListingHandler) List(
	ctx context.Context,
	_ *connect.Request[resalelistingv1.ListRequest],
) (*connect.Response[resalelistingv1.ListResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	listings, err := h.resaleListingUC.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&resalelistingv1.ListResponse{ResaleListings: mapper.ResaleListingsToProto(listings)}), nil
}
