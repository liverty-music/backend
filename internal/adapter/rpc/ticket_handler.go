package rpc

import (
	"context"
	"errors"
	"time"

	ticketv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket/v1/ticketv1connect"
	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that TicketHandler satisfies the generated interface.
var _ ticketv1connect.TicketServiceHandler = (*TicketHandler)(nil)

// TicketHandler implements the TicketService Connect interface — the buyer-facing
// read surface over a caller's own Orders and issued Tickets.
type TicketHandler struct {
	ticketUC usecase.TicketUseCase
	walletUC usecase.WalletPublicKeyUseCase
	userRepo entity.UserRepository
	logger   *logging.Logger
}

// NewTicketHandler creates a new instance of the ticket RPC service handler.
func NewTicketHandler(
	ticketUC usecase.TicketUseCase,
	walletUC usecase.WalletPublicKeyUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *TicketHandler {
	return &TicketHandler{
		ticketUC: ticketUC,
		walletUC: walletUC,
		userRepo: userRepo,
		logger:   logger,
	}
}

// GetOrder returns one of the caller's own Orders.
func (h *TicketHandler) GetOrder(ctx context.Context, req *connect.Request[rpc.GetOrderRequest]) (*connect.Response[rpc.GetOrderResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	orderID := req.Msg.GetOrderId().GetValue()
	if orderID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("order_id is required"))
	}
	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	order, err := h.ticketUC.GetOrder(ctx, entity.UserID(user.ID), entity.OrderID(orderID))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.GetOrderResponse{Order: mapper.OrderToProto(order)}), nil
}

// List returns all account-bound tickets issued to the caller.
func (h *TicketHandler) List(ctx context.Context, _ *connect.Request[rpc.ListRequest]) (*connect.Response[rpc.ListResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	tickets, err := h.ticketUC.GetMyTickets(ctx, entity.UserID(user.ID))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.ListResponse{Tickets: mapper.TicketsToProto(tickets)}), nil
}

// RegisterWalletPublicKey records the public key of the caller's device as
// the caller's WalletPublicKey, replacing any other device's key.
func (h *TicketHandler) RegisterWalletPublicKey(ctx context.Context, req *connect.Request[rpc.RegisterWalletPublicKeyRequest]) (*connect.Response[rpc.RegisterWalletPublicKeyResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Msg.GetPublicKey().GetValue()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("public_key is required"))
	}
	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	result, err := h.walletUC.Register(ctx, entity.UserID(user.ID), entity.PublicKey(req.Msg.GetPublicKey().GetValue()), time.Now())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.RegisterWalletPublicKeyResponse{
		WalletPublicKey:  mapper.WalletPublicKeyToProto(result.Key),
		ReplacedOtherKey: result.ReplacedOtherKey,
	}), nil
}
