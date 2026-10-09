package rpc

import (
	"context"
	"errors"

	reservationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/reservation/v1/reservationv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	reservationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/reservation/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that ReservationHandler satisfies the generated
// interface.
var _ reservationv1connect.ReservationServiceHandler = (*ReservationHandler)(nil)

// ReservationHandler implements the fan-facing ReservationService: the
// signed-in fan's checkout. The fan is always the caller, resolved from the
// token to their stored User; a request never names a User.
type ReservationHandler struct {
	reservationUC usecase.ReservationUseCase
	issuanceUC    usecase.IssuanceUseCase
	userRepo      entity.UserRepository
	logger        *logging.Logger
}

// NewReservationHandler creates a new ReservationHandler.
func NewReservationHandler(
	reservationUC usecase.ReservationUseCase,
	issuanceUC usecase.IssuanceUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *ReservationHandler {
	return &ReservationHandler{reservationUC: reservationUC, issuanceUC: issuanceUC, userRepo: userRepo, logger: logger}
}

// caller resolves the signed-in caller to their stored User id. It fails with
// Unauthenticated without a sign-in and NotFound without a stored account.
func (h *ReservationHandler) caller(ctx context.Context) (entity.UserID, error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return "", err
	}
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return "", err
	}
	return entity.UserID(user.ID), nil
}

// Start holds tickets of a sale for the caller for 15 minutes.
func (h *ReservationHandler) Start(
	ctx context.Context,
	req *connect.Request[reservationv1.StartRequest],
) (*connect.Response[reservationv1.StartResponse], error) {
	userID, err := h.caller(ctx)
	if err != nil {
		return nil, err
	}
	saleID := req.Msg.GetTicketSaleId().GetValue()
	if err := requireUUID("ticket_sale_id", saleID); err != nil {
		return nil, err
	}
	if req.Msg.GetTicketCount() < 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("ticket_count is required"))
	}

	started, err := h.reservationUC.Start(ctx, userID, entity.TicketSaleID(saleID), int(req.Msg.GetTicketCount()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&reservationv1.StartResponse{
		Reservation:         mapper.ReservationToProto(started.Reservation),
		TicketPrice:         started.TicketPrice,
		SavedHolderIdentity: mapper.HolderIdentityToProto(started.SavedIdentity),
	}), nil
}

// Get returns the caller's checkout and what became of it.
func (h *ReservationHandler) Get(
	ctx context.Context,
	req *connect.Request[reservationv1.GetRequest],
) (*connect.Response[reservationv1.GetResponse], error) {
	userID, err := h.caller(ctx)
	if err != nil {
		return nil, err
	}
	id := req.Msg.GetReservationId().GetValue()
	if err := requireUUID("reservation_id", id); err != nil {
		return nil, err
	}

	view, err := h.reservationUC.Get(ctx, userID, entity.ReservationID(id))
	if err != nil {
		return nil, err
	}
	resp := &reservationv1.GetResponse{
		Reservation: mapper.ReservationToProto(view.Reservation),
		Holding:     view.Holding,
		Authorized:  view.Authorized,
	}
	if view.OrderID != "" {
		resp.OrderId = &entityv1.OrderId{Value: string(view.OrderID)}
	}
	return connect.NewResponse(resp), nil
}

// Authorize records the holder identity and opens the card hold.
func (h *ReservationHandler) Authorize(
	ctx context.Context,
	req *connect.Request[reservationv1.AuthorizeRequest],
) (*connect.Response[reservationv1.AuthorizeResponse], error) {
	userID, err := h.caller(ctx)
	if err != nil {
		return nil, err
	}
	id := req.Msg.GetReservationId().GetValue()
	if err := requireUUID("reservation_id", id); err != nil {
		return nil, err
	}
	identity := req.Msg.GetHolderIdentity()
	if identity.GetFullName() == "" || identity.GetPhoneNumber() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holder name and phone number are required"))
	}

	secret, err := h.reservationUC.Authorize(ctx, userID, entity.ReservationID(id), mapper.HolderIdentityFromProto(identity))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&reservationv1.AuthorizeResponse{ClientSecret: secret}), nil
}

// Confirm places the order for the caller's checkout.
func (h *ReservationHandler) Confirm(
	ctx context.Context,
	req *connect.Request[reservationv1.ConfirmRequest],
) (*connect.Response[reservationv1.ConfirmResponse], error) {
	userID, err := h.caller(ctx)
	if err != nil {
		return nil, err
	}
	id := req.Msg.GetReservationId().GetValue()
	if err := requireUUID("reservation_id", id); err != nil {
		return nil, err
	}

	order, err := h.issuanceUC.IssueFromReservation(ctx, entity.ReservationID(id), &userID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&reservationv1.ConfirmResponse{Order: mapper.OrderToProto(order)}), nil
}
