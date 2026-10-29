package entity

import (
	"context"
	"errors"
	"time"
)

// TicketStockID is the opaque identifier for a single TicketStock.
//
// Mirrors liverty_music.entity.v1.TicketStockId.value (a UUID string).
type TicketStockID string

// TicketStockState is where a TicketStock stands at a given time. It is
// derived, never stored. Values mirror the proto enum
// liverty_music.entity.v1.TicketStockState.
type TicketStockState int16

const (
	// TicketStockStateUnspecified is the zero value and is never returned.
	TicketStockStateUnspecified TicketStockState = 0
	// TicketStockStateAvailable means tickets remain.
	TicketStockStateAvailable TicketStockState = 1
	// TicketStockStateAllHeld means no ticket remains, but some are held by
	// Checkouts and not sold.
	TicketStockStateAllHeld TicketStockState = 2
	// TicketStockStateSoldOut means the sold count equals the quantity.
	TicketStockStateSoldOut TicketStockState = 3
)

// TicketStock is the tickets of one SeatType for one event: how many there
// are, and how many are sold or held at a time. Every TicketSale of the event
// sells from it, a fan's Order names it, and its official resale (公式リセール)
// opens, for listing tickets, when it sells out. Its event, seat type and
// quantity never change once it is created.
//
// Mirrors liverty_music.entity.v1.TicketStock.
type TicketStock struct {
	// ID is the surrogate primary key (UUIDv7).
	ID TicketStockID
	// EventID is the event its tickets admit to: an event of the seat type's
	// Series.
	EventID string
	// SeatTypeID is the SeatType of its tickets.
	SeatTypeID SeatTypeID
	// Quantity is the number of tickets in the stock.
	Quantity int

	// SoldCount is the tickets of the stock's Orders that are Paid or
	// Refunded, or Pending with a Committed Checkout or a Won LotteryEntry, at
	// the time it was read. Aggregated by the repository, never stored.
	SoldCount int
	// HeldCount is the tickets of the stock's Pending Orders whose Checkout is
	// holding at the time it was read. Aggregated by the repository, never
	// stored.
	HeldCount int

	// SeatType is the stock's SeatType, read with every TicketStock.
	SeatType *SeatType
}

// NewTicketStock returns a TicketStock of the seat type for the event with a
// generated UUIDv7 id and nothing sold or held.
func NewTicketStock(eventID string, seatTypeID SeatTypeID, quantity int) *TicketStock {
	return &TicketStock{
		ID:         TicketStockID(NewID()),
		EventID:    eventID,
		SeatTypeID: seatTypeID,
		Quantity:   quantity,
	}
}

// Validate reports whether the quantity is at least 1. The entity layer
// returns stdlib errors; callers wrap them with an apperr code.
func (s *TicketStock) Validate() error {
	if s.Quantity < 1 {
		return errors.New("quantity must be at least 1")
	}
	return nil
}

// Remaining returns the tickets left: the quantity minus the sold count minus
// the held count, never below 0.
func (s *TicketStock) Remaining() int {
	return max(s.Quantity-s.SoldCount-s.HeldCount, 0)
}

// State returns where the stock stands with its sold and held counts:
// SoldOut when the sold count equals the quantity, AllHeld when no ticket
// remains but some are held, Available otherwise.
func (s *TicketStock) State() TicketStockState {
	switch {
	case s.SoldCount >= s.Quantity:
		return TicketStockStateSoldOut
	case s.Remaining() == 0:
		return TicketStockStateAllHeld
	default:
		return TicketStockStateAvailable
	}
}

// IsLowStock reports whether the stock is Available with at most one tenth of
// its quantity remaining, rounded up, so a stock of 9 is LowStock with 1 left.
func (s *TicketStock) IsLowStock() bool {
	if s.State() != TicketStockStateAvailable {
		return false
	}
	return s.Remaining() <= (s.Quantity+9)/10
}

// IsResaleOpenAt reports whether the stock's official resale is open at time
// at, given its event's resale deadline: the stock is SoldOut and at is before
// the deadline.
func (s *TicketStock) IsResaleOpenAt(at, deadline time.Time) bool {
	return s.State() == TicketStockStateSoldOut && at.Before(deadline)
}

// TicketStockRepository persists TicketStocks and reads their sold and held
// counts from their Orders. Every read returns the stock with its SeatType and
// its counts at the given time. Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type TicketStockRepository interface {
	// Create stores the TicketStock and returns it, with nothing sold or held.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the quantity breaks the TicketStock rules, or the
	//    event is not of the SeatType's Series.
	//  - FailedPrecondition: the event or the SeatType does not exist.
	//  - AlreadyExists: the event already has a TicketStock of the SeatType.
	//  - Internal: database failure.
	Create(ctx context.Context, stock *TicketStock) (*TicketStock, error)

	// Get returns the TicketStock with the given id.
	//
	// # Possible errors
	//
	//  - NotFound: no TicketStock has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id TicketStockID, at time.Time) (*TicketStock, error)

	// Delete removes the TicketStock together with the Void Orders that name it
	// and their LotteryEntries and Checkouts, all together or not at all,
	// under the stock's row lock.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: an Order that is not Void names the stock, also
	//    one created while Delete runs, or a Void Order's card authorization
	//    is not yet released; nothing is removed.
	//  - NotFound: no TicketStock has the id.
	//  - Internal: database failure.
	Delete(ctx context.Context, id TicketStockID) error

	// ListByEvent returns every TicketStock of the event, ordered by its
	// SeatType's price, highest first, then by its name. It returns an empty
	// slice when the event has none or does not exist.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByEvent(ctx context.Context, eventID string, at time.Time) ([]*TicketStock, error)
}
