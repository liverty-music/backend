package rpc

import (
	"context"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"

	lotteryentryv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/lottery_entry/v1/lottery_entryv1connect"
	lotteryentryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/lottery_entry/v1"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that LotteryEntryHandler satisfies the generated
// interface.
var _ lotteryentryv1connect.LotteryEntryServiceHandler = (*LotteryEntryHandler)(nil)

// LotteryEntryHandler implements the fan-facing LotteryEntryService: a
// signed-in fan authorizes their card for a ticket count, confirms the entry
// and reads their own lottery entries. The fan is always the caller; a request never names a
// User. Requests are validated by the protovalidate interceptor before this
// handler runs.
type LotteryEntryHandler struct {
	lotteryEntryUC usecase.LotteryEntryUseCase
	userUC         usecase.UserUseCase
	logger         *logging.Logger
}

// NewLotteryEntryHandler creates a new LotteryEntryHandler.
func NewLotteryEntryHandler(
	lotteryEntryUC usecase.LotteryEntryUseCase,
	userUC usecase.UserUseCase,
	logger *logging.Logger,
) *LotteryEntryHandler {
	return &LotteryEntryHandler{lotteryEntryUC: lotteryEntryUC, userUC: userUC, logger: logger}
}

// callerUserID resolves the signed-in caller to their stored User id. It fails
// with Unauthenticated without a sign-in and NotFound without a stored
// account.
func callerUserID(ctx context.Context, userUC usecase.UserUseCase) (entity.UserID, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return "", err
	}
	user, err := userUC.ResolveCaller(ctx, providerUserID)
	if err != nil {
		return "", err
	}
	return entity.UserID(user.ID), nil
}

// AuthorizeCard stores the caller's full name and phone number and opens the
// card authorization for a lottery entry of a ticket count, returning its id
// and its card authorization secret.
func (h *LotteryEntryHandler) AuthorizeCard(
	ctx context.Context,
	req *lotteryentryv1.AuthorizeCardRequest,
) (*lotteryentryv1.AuthorizeCardResponse, error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	authorization, err := h.lotteryEntryUC.AuthorizeCard(ctx, entity.TicketSaleID(req.GetTicketSaleId().GetValue()),
		entity.TicketStockID(req.GetTicketStockId().GetValue()), userID,
		int(req.GetTicketCount()), req.GetFullName().GetValue(), req.GetPhoneNumber().GetValue(),
		mapper.ReferralCodeFromProto(req.GetReferralCode()))
	if err != nil {
		return nil, err
	}
	return &lotteryentryv1.AuthorizeCardResponse{
		CardAuthorizationId:     &entityv1.CardAuthorizationId{Value: authorization.ID},
		CardAuthorizationSecret: &entityv1.CardAuthorizationSecret{Value: authorization.Secret},
	}, nil
}

// Confirm makes the caller's entry from their authorized card authorization.
func (h *LotteryEntryHandler) Confirm(
	ctx context.Context,
	req *lotteryentryv1.ConfirmRequest,
) (*lotteryentryv1.ConfirmResponse, error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	entry, err := h.lotteryEntryUC.Confirm(ctx, req.GetCardAuthorizationId().GetValue(), &userID)
	if err != nil {
		return nil, err
	}
	return &lotteryentryv1.ConfirmResponse{LotteryEntry: mapper.LotteryEntryToProto(entry), Order: mapper.OrderToProto(entry.Order)}, nil
}

// ListByTicketType returns the caller's entries in a lottery sale, newest
// first, with their Orders.
func (h *LotteryEntryHandler) ListByTicketType(
	ctx context.Context,
	req *lotteryentryv1.ListByTicketTypeRequest,
) (*lotteryentryv1.ListByTicketTypeResponse, error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	entries, err := h.lotteryEntryUC.ListByTicketType(ctx, entity.TicketSaleID(req.GetTicketSaleId().GetValue()), userID)
	if err != nil {
		return nil, err
	}
	orders := make([]*entityv1.Order, 0, len(entries))
	for _, e := range entries {
		if o := mapper.OrderToProto(e.Order); o != nil {
			orders = append(orders, o)
		}
	}
	return &lotteryentryv1.ListByTicketTypeResponse{LotteryEntries: mapper.LotteryEntriesToProto(entries), Orders: orders}, nil
}
