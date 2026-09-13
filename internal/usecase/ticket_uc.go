package usecase

import (
	"context"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// TicketUseCase is the buyer-facing read surface over a caller's own Orders and
// issued Tickets.
type TicketUseCase interface {
	// GetOrder returns one of the caller's own Orders. An Order belonging to a
	// different account is reported as NotFound (non-revealing), never disclosed.
	//
	// # Possible errors
	//
	//  - NotFound: no such Order for this caller.
	//  - Internal: database query failure.
	GetOrder(ctx context.Context, buyerID entity.UserID, orderID entity.OrderID) (*entity.Order, error)

	// GetMyTickets returns all tickets currently bound to the caller's account.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	GetMyTickets(ctx context.Context, holderID entity.UserID) ([]*entity.Ticket, error)
}

// ticketUseCase implements [TicketUseCase].
type ticketUseCase struct {
	orderRepo  entity.OrderRepository
	ticketRepo entity.TicketRepository
	logger     *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketUseCase = (*ticketUseCase)(nil)

// NewTicketUseCase constructs a TicketUseCase. All parameters are required.
func NewTicketUseCase(
	orderRepo entity.OrderRepository,
	ticketRepo entity.TicketRepository,
	logger *logging.Logger,
) TicketUseCase {
	return &ticketUseCase{
		orderRepo:  orderRepo,
		ticketRepo: ticketRepo,
		logger:     logger,
	}
}

// GetOrder implements [TicketUseCase].
func (uc *ticketUseCase) GetOrder(ctx context.Context, buyerID entity.UserID, orderID entity.OrderID) (*entity.Order, error) {
	order, err := uc.orderRepo.Get(ctx, orderID)
	if err != nil {
		return nil, err // propagates NotFound
	}
	// Ownership check: an Order belonging to another account is reported exactly
	// like a missing one (non-revealing), never disclosed.
	if order.BuyerID != buyerID {
		return nil, apperr.New(codes.NotFound, "order not found")
	}
	return order, nil
}

// GetMyTickets implements [TicketUseCase].
func (uc *ticketUseCase) GetMyTickets(ctx context.Context, holderID entity.UserID) ([]*entity.Ticket, error) {
	return uc.ticketRepo.ListByHolder(ctx, holderID)
}
