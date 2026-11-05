package rpc

import (
	"context"

	resaleentryv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/resale_entry/v1/resale_entryv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	resaleentryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/resale_entry/v1"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that ResaleEntryHandler satisfies the generated
// interface.
var _ resaleentryv1connect.ResaleEntryServiceHandler = (*ResaleEntryHandler)(nil)

// ResaleEntryHandler implements the fan-facing ResaleEntryService: a
// signed-in fan authorizes their card for an official resale application,
// confirms the entry, reads their entries and withdraws a waiting one. The fan
// is always the caller; a request never names a User. Requests are validated
// by the protovalidate interceptor before this handler runs.
type ResaleEntryHandler struct {
	resaleEntryUC usecase.ResaleEntryUseCase
	userUC        usecase.UserUseCase
	logger        *logging.Logger
}

// NewResaleEntryHandler creates a new ResaleEntryHandler.
func NewResaleEntryHandler(resaleEntryUC usecase.ResaleEntryUseCase, userUC usecase.UserUseCase, logger *logging.Logger) *ResaleEntryHandler {
	return &ResaleEntryHandler{resaleEntryUC: resaleEntryUC, userUC: userUC, logger: logger}
}

// AuthorizeCard stores the caller's full name and phone number and opens the
// card authorization of a resale application, returning its id and secret.
func (h *ResaleEntryHandler) AuthorizeCard(ctx context.Context, req *resaleentryv1.AuthorizeCardRequest) (*resaleentryv1.AuthorizeCardResponse, error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}
	authorization, err := h.resaleEntryUC.AuthorizeCard(ctx, entity.TicketStockID(req.GetTicketStockId().GetValue()), userID,
		int(req.GetTicketCount()), req.GetFullName().GetValue(), req.GetPhoneNumber().GetValue(), pb.ToReferralCode(req.GetReferralCode()))
	if err != nil {
		return nil, err
	}
	return &resaleentryv1.AuthorizeCardResponse{
		CardAuthorizationId:     &entityv1.CardAuthorizationId{Value: authorization.ID},
		CardAuthorizationSecret: &entityv1.CardAuthorizationSecret{Value: authorization.Secret},
	}, nil
}

// Confirm makes the caller's entry from their authorized card authorization.
func (h *ResaleEntryHandler) Confirm(ctx context.Context, req *resaleentryv1.ConfirmRequest) (*resaleentryv1.ConfirmResponse, error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}
	view, err := h.resaleEntryUC.Confirm(ctx, req.GetCardAuthorizationId().GetValue(), &userID)
	if err != nil {
		return nil, err
	}
	return &resaleentryv1.ConfirmResponse{
		ResaleEntry: pb.FromResaleEntry(view.Entry, view.NextDrawTime),
		Order:       pb.FromOrder(view.Entry.Order),
	}, nil
}

// List returns the caller's entries, newest first, with their Orders, stocks
// and seat types.
func (h *ResaleEntryHandler) List(ctx context.Context, _ *resaleentryv1.ListRequest) (*resaleentryv1.ListResponse, error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}
	views, err := h.resaleEntryUC.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	resp := &resaleentryv1.ListResponse{}
	stocks := map[entity.TicketStockID]bool{}
	seatTypes := map[entity.SeatTypeID]bool{}
	for _, v := range views {
		resp.ResaleEntries = append(resp.ResaleEntries, pb.FromResaleEntry(v.Entry, v.NextDrawTime))
		resp.Orders = append(resp.Orders, pb.FromOrder(v.Entry.Order))
		if !stocks[v.Stock.ID] {
			stocks[v.Stock.ID] = true
			resp.TicketStocks = append(resp.TicketStocks, pb.FromTicketStock(v.Stock, false, false))
		}
		if v.Stock.SeatType != nil && !seatTypes[v.Stock.SeatType.ID] {
			seatTypes[v.Stock.SeatType.ID] = true
			resp.SeatTypes = append(resp.SeatTypes, pb.FromSeatType(v.Stock.SeatType))
		}
	}
	return resp, nil
}

// Withdraw withdraws the caller's waiting entry.
func (h *ResaleEntryHandler) Withdraw(ctx context.Context, req *resaleentryv1.WithdrawRequest) (*resaleentryv1.WithdrawResponse, error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}
	entry, err := h.resaleEntryUC.Withdraw(ctx, userID, entity.ResaleEntryID(req.GetResaleEntryId().GetValue()))
	if err != nil {
		return nil, err
	}
	return &resaleentryv1.WithdrawResponse{ResaleEntry: pb.FromResaleEntry(entry, nil)}, nil
}
