package rpc

import (
	"context"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"

	lotteryentryv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/lottery_entry/v1/lottery_entryv1connect"
	lotteryentryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/lottery_entry/v1"
	"connectrpc.com/connect"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that LotteryEntryHandler satisfies the generated
// interface.
var _ lotteryentryv1connect.LotteryEntryServiceHandler = (*LotteryEntryHandler)(nil)

// LotteryEntryHandler implements the fan-facing LotteryEntryService: a
// signed-in fan starts, authorizes, confirms, withdraws and reads their own
// lottery entries. The fan is always the caller; a request never names a
// User. Requests are validated by the protovalidate interceptor before this
// handler runs.
type LotteryEntryHandler struct {
	lotteryUC usecase.LotteryUseCase
	userUC    usecase.UserUseCase
	logger    *logging.Logger
}

// NewLotteryEntryHandler creates a new LotteryEntryHandler.
func NewLotteryEntryHandler(
	lotteryUC usecase.LotteryUseCase,
	userUC usecase.UserUseCase,
	logger *logging.Logger,
) *LotteryEntryHandler {
	return &LotteryEntryHandler{lotteryUC: lotteryUC, userUC: userUC, logger: logger}
}

// callerUserID resolves the signed-in caller to their stored User id. It fails
// with Unauthenticated without a sign-in and NotFound without a stored
// account.
func callerUserID(ctx context.Context, userUC usecase.UserUseCase) (entity.UserID, error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return "", err
	}
	user, err := userUC.ResolveCaller(ctx, externalID)
	if err != nil {
		return "", err
	}
	return entity.UserID(user.ID), nil
}

// Start begins the caller's Draft entry for a TicketType, or resumes it.
func (h *LotteryEntryHandler) Start(
	ctx context.Context,
	req *connect.Request[lotteryentryv1.StartRequest],
) (*connect.Response[lotteryentryv1.StartResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	entry, err := h.lotteryUC.Start(ctx, entity.TicketTypeID(req.Msg.GetTicketTypeId().GetValue()), userID, int(req.Msg.GetTicketCount()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&lotteryentryv1.StartResponse{LotteryEntry: mapper.LotteryEntryToProto(entry)}), nil
}

// AuthorizeCard stores the caller's full name and phone number and opens the card
// hold for their Draft, returning the hold's confirmation secret.
func (h *LotteryEntryHandler) AuthorizeCard(
	ctx context.Context,
	req *connect.Request[lotteryentryv1.AuthorizeCardRequest],
) (*connect.Response[lotteryentryv1.AuthorizeCardResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	secret, err := h.lotteryUC.Authorize(ctx, entity.LotteryEntryID(req.Msg.GetLotteryEntryId().GetValue()), userID,
		req.Msg.GetFullName().GetValue(), req.Msg.GetPhoneNumber().GetValue())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&lotteryentryv1.AuthorizeCardResponse{CardAuthorizationSecret: &entityv1.CardAuthorizationSecret{Value: secret}}), nil
}

// Confirm makes the caller's authorized Draft Entered.
func (h *LotteryEntryHandler) Confirm(
	ctx context.Context,
	req *connect.Request[lotteryentryv1.ConfirmRequest],
) (*connect.Response[lotteryentryv1.ConfirmResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	entry, err := h.lotteryUC.Confirm(ctx, entity.LotteryEntryID(req.Msg.GetLotteryEntryId().GetValue()), userID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&lotteryentryv1.ConfirmResponse{LotteryEntry: mapper.LotteryEntryToProto(entry)}), nil
}

// Withdraw withdraws the caller's own Draft or Entered entry.
func (h *LotteryEntryHandler) Withdraw(
	ctx context.Context,
	req *connect.Request[lotteryentryv1.WithdrawRequest],
) (*connect.Response[lotteryentryv1.WithdrawResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	if err := h.lotteryUC.WithdrawEntry(ctx, entity.LotteryEntryID(req.Msg.GetLotteryEntryId().GetValue()), userID); err != nil {
		return nil, err
	}
	return connect.NewResponse(&lotteryentryv1.WithdrawResponse{}), nil
}

// ListByTicketType returns the caller's entries for a TicketType, withdrawn
// ones included, newest first.
func (h *LotteryEntryHandler) ListByTicketType(
	ctx context.Context,
	req *connect.Request[lotteryentryv1.ListByTicketTypeRequest],
) (*connect.Response[lotteryentryv1.ListByTicketTypeResponse], error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}

	entries, err := h.lotteryUC.ListOwnByTicketType(ctx, entity.TicketTypeID(req.Msg.GetTicketTypeId().GetValue()), userID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&lotteryentryv1.ListByTicketTypeResponse{LotteryEntries: mapper.LotteryEntriesToProto(entries)}), nil
}
