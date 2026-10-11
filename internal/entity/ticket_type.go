package entity

import (
	"errors"
	"fmt"
	"time"
)

// TicketTypeID is the opaque identifier for a single TicketType.
//
// Mirrors liverty_music.entity.v1.TicketTypeId.value (a UUID string).
type TicketTypeID string

// TicketTypeState is where a FirstCome TicketType stands at a given time. It
// is derived, never stored. Values mirror the proto enum
// liverty_music.entity.v1.TicketTypeState.
type TicketTypeState int16

const (
	// TicketTypeStateUnspecified is the zero value and is never returned.
	TicketTypeStateUnspecified TicketTypeState = 0
	// TicketTypeStateNotYetOnSale is before the sale's start time.
	TicketTypeStateNotYetOnSale TicketTypeState = 1
	// TicketTypeStateOnSale is inside the sale's window with tickets remaining.
	TicketTypeStateOnSale TicketTypeState = 2
	// TicketTypeStateAllHeld is inside the sale's window, not sold out, but
	// every remaining ticket is held by checkouts.
	TicketTypeStateAllHeld TicketTypeState = 3
	// TicketTypeStateSoldOut is inside the sale's window with the sold count
	// equal to the quantity.
	TicketTypeStateSoldOut TicketTypeState = 4
	// TicketTypeStateEnded is at or after the sale's end time.
	TicketTypeStateEnded TicketTypeState = 5
)

const (
	// TicketTypeMinPrice is the lowest tax-inclusive ticket price in yen.
	TicketTypeMinPrice int64 = 1
	// TicketTypeMaxPrice is the highest tax-inclusive ticket price in yen.
	TicketTypeMaxPrice int64 = 1_000_000
	// TicketTypeDefaultPerAccountLimit is the per-account limit of a
	// TicketType created without one.
	TicketTypeDefaultPerAccountLimit = 4
	// TicketTypeMaxPerAccountLimit is the highest per-account limit allowed:
	// one admission code presents at most 10 tickets.
	TicketTypeMaxPerAccountLimit = 10
)

// TicketType is what one TicketSale offers for one event: the price of one
// ticket, how many tickets are offered and how many tickets one account may
// hold for the event. For a FirstCome sale it also carries that event's
// stock: the tickets sold and the tickets held in checkouts.
//
// Mirrors liverty_music.entity.v1.TicketType.
type TicketType struct {
	// ID is the surrogate primary key (UUIDv7).
	ID TicketTypeID
	// TicketSaleID is the sale that offers it.
	TicketSaleID TicketSaleID
	// EventID is the event it admits to: an event of the sale's Series.
	EventID string
	// Price is one ticket's tax-inclusive (税込) price in yen.
	Price int64
	// Quantity is the number of tickets offered.
	Quantity int
	// PerAccountLimit is the most tickets one account may hold for the event
	// once this sale is done, counting the tickets it already holds.
	PerAccountLimit int
	// SoldCount is the number of tickets on the TicketType's Committed and
	// Completed Checkouts. FirstCome only; always 0 for a Lottery TicketType.
	SoldCount int

	// HeldCount is the number of tickets on the TicketType's Checkouts that
	// are holding at the time it was read. FirstCome only. It is computed by
	// the repository on every read, never stored.
	HeldCount int
}

// NewTicketType returns a TicketType of sale for the event, with a generated
// UUIDv7 id and a sold count of 0, and appends it to sale.TicketTypes. A
// perAccountLimit of 0 means none was given, and the limit is
// [TicketTypeDefaultPerAccountLimit].
func NewTicketType(sale *TicketSale, eventID string, price int64, quantity, perAccountLimit int) *TicketType {
	if perAccountLimit == 0 {
		perAccountLimit = TicketTypeDefaultPerAccountLimit
	}
	tt := &TicketType{
		ID:              TicketTypeID(NewID()),
		TicketSaleID:    sale.ID,
		EventID:         eventID,
		Price:           price,
		Quantity:        quantity,
		PerAccountLimit: perAccountLimit,
	}
	sale.TicketTypes = append(sale.TicketTypes, tt)
	return tt
}

// Validate reports whether the price is 1 to 1,000,000 yen, the quantity is
// at least 1, and the per-account limit is 1 to 10 and at most the quantity.
// The entity layer returns stdlib errors; callers wrap them with an apperr
// code.
func (t *TicketType) Validate() error {
	if t.Price < TicketTypeMinPrice || t.Price > TicketTypeMaxPrice {
		return errors.New("price must be 1 to 1,000,000 yen")
	}
	if t.Quantity < 1 {
		return errors.New("quantity must be at least 1")
	}
	if t.PerAccountLimit < 1 || t.PerAccountLimit > TicketTypeMaxPerAccountLimit {
		return errors.New("per-account limit must be 1 to 10")
	}
	if t.PerAccountLimit > t.Quantity {
		return errors.New("per-account limit must not exceed the quantity")
	}
	return nil
}

// ValidateTicketCount reports whether a requested ticket count is at least 1
// and at most the per-account limit.
func (t *TicketType) ValidateTicketCount(count int) error {
	if count < 1 || count > t.PerAccountLimit {
		return fmt.Errorf("ticket count must be 1 to %d", t.PerAccountLimit)
	}
	return nil
}

// AmountFor returns the amount in yen for count tickets: the price times the
// count.
func (t *TicketType) AmountFor(count int) int64 {
	return t.Price * int64(count)
}

// FitsPerAccountLimit reports whether an account that already holds held
// tickets for the event may get requested more: held plus requested is at
// most the per-account limit.
func (t *TicketType) FitsPerAccountLimit(held, requested int) bool {
	return held+requested <= t.PerAccountLimit
}

// Remaining returns the tickets left to hold: the quantity minus the sold
// count minus the held count, never below 0.
func (t *TicketType) Remaining() int {
	return max(t.Quantity-t.SoldCount-t.HeldCount, 0)
}

// StateAt returns the TicketType's state at time at, from sale's window and
// the TicketType's sold and held counts. Ended takes precedence over SoldOut
// and AllHeld.
func (t *TicketType) StateAt(sale *TicketSale, at time.Time) TicketTypeState {
	switch {
	case at.Before(sale.SaleStartTime):
		return TicketTypeStateNotYetOnSale
	case !at.Before(sale.SaleEndTime):
		return TicketTypeStateEnded
	case t.SoldCount >= t.Quantity:
		return TicketTypeStateSoldOut
	case t.Remaining() == 0:
		return TicketTypeStateAllHeld
	default:
		return TicketTypeStateOnSale
	}
}

// IsLowStockAt reports whether the TicketType is OnSale at time at with at
// most one tenth of its quantity remaining, rounded up — so a TicketType of 9
// is LowStock with 1 left.
func (t *TicketType) IsLowStockAt(sale *TicketSale, at time.Time) bool {
	if t.StateAt(sale, at) != TicketTypeStateOnSale {
		return false
	}
	threshold := (t.Quantity + 9) / 10
	return t.Remaining() <= threshold
}

// ValidatePriceChange reports whether the price of a FirstCome TicketType may
// change to newPrice: it may only while nothing is sold or held, so every fan
// who pays for its tickets pays the same price. Keeping the same price is
// always allowed.
func (t *TicketType) ValidatePriceChange(newPrice int64) error {
	if newPrice != t.Price && t.SoldCount+t.HeldCount > 0 {
		return errors.New("the price cannot change while tickets are sold or held")
	}
	return nil
}
