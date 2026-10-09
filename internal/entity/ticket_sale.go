package entity

import (
	"context"
	"errors"
	"time"
)

// TicketSaleID is the opaque identifier for a single TicketSale.
//
// Mirrors liverty_music.entity.v1.TicketSaleId.value (a UUID string).
type TicketSaleID string

// TicketSaleMethod is how a TicketSale allocates its tickets. Values mirror
// the proto enum liverty_music.entity.v1.TicketSaleMethod.
type TicketSaleMethod int16

const (
	// TicketSaleMethodUnspecified is the zero value and is never persisted.
	TicketSaleMethodUnspecified TicketSaleMethod = 0
	// TicketSaleMethodFirstCome is first come, first served: a fan who starts a
	// checkout while tickets remain holds them and buys them by completing it.
	TicketSaleMethodFirstCome TicketSaleMethod = 1
)

// TicketSaleState is where a TicketSale stands at a given time. It is derived,
// never stored. Values mirror the proto enum liverty_music.entity.v1.TicketSaleState.
type TicketSaleState int16

const (
	// TicketSaleStateUnspecified is the zero value and is never returned.
	TicketSaleStateUnspecified TicketSaleState = 0
	// TicketSaleStateNotYetOnSale is before the sale start.
	TicketSaleStateNotYetOnSale TicketSaleState = 1
	// TicketSaleStateOnSale is inside the sale window with tickets remaining.
	TicketSaleStateOnSale TicketSaleState = 2
	// TicketSaleStateAllHeld is inside the sale window, not sold out, but every
	// remaining ticket is held by checkouts.
	TicketSaleStateAllHeld TicketSaleState = 3
	// TicketSaleStateSoldOut is inside the sale window with the sold count equal
	// to the quantity.
	TicketSaleStateSoldOut TicketSaleState = 4
	// TicketSaleStateEnded is at or after the sale end.
	TicketSaleStateEnded TicketSaleState = 5
)

const (
	// TicketSaleMinPrice is the lowest tax-inclusive ticket price in yen.
	TicketSaleMinPrice int64 = 1
	// TicketSaleMaxPrice is the highest tax-inclusive ticket price in yen.
	TicketSaleMaxPrice int64 = 1_000_000
	// TicketSaleDefaultPerAccountLimit is the per-account limit of a sale
	// created without one.
	TicketSaleDefaultPerAccountLimit = 4
	// TicketSaleMaxPerAccountLimit is the highest per-account limit allowed.
	TicketSaleMaxPerAccountLimit = 10
)

// TicketSale is the platform's own sale of one event's tickets, first come,
// first served: an Organizer offers a quantity of tickets at one tax-inclusive
// price during a sale window, and each fan may buy up to a per-account limit.
//
// Mirrors liverty_music.entity.v1.TicketSale.
type TicketSale struct {
	// ID is the surrogate primary key (UUIDv7).
	ID TicketSaleID
	// EventID is the event whose tickets are sold; one TicketSale per event.
	EventID string
	// Method is how tickets are allocated (FirstCome).
	Method TicketSaleMethod
	// SaleStartTime is when the sale opens.
	SaleStartTime time.Time
	// SaleEndTime is when the sale closes: after SaleStartTime and no later than
	// the event's start time (checked by the usecase, which reads the event).
	SaleEndTime time.Time
	// Price is one ticket's tax-inclusive (税込) price in yen.
	Price int64
	// Quantity is the number of tickets offered.
	Quantity int
	// PerAccountLimit is the most tickets one User may hold or have bought.
	PerAccountLimit int
	// SoldCount is the number of tickets on the sale's Committed and Completed
	// Reservations.
	SoldCount int
	// CreateTime is when the sale was set up.
	CreateTime time.Time

	// HeldCount is the number of tickets on the sale's Reservations that are
	// holding at the time the sale was read. It is computed by the repository
	// on every read, never stored.
	HeldCount int
	// HasReservations reports whether any Reservation was ever created for the
	// sale, which fixes its price. Computed by the repository on every read.
	HasReservations bool
}

// NewTicketSale returns a FirstCome TicketSale with a generated UUIDv7 id, a
// sold count of 0 and the given created time. A perAccountLimit of 0 means
// none was given, and the limit is [TicketSaleDefaultPerAccountLimit].
func NewTicketSale(eventID string, saleStart, saleEnd time.Time, price int64, quantity, perAccountLimit int, now time.Time) *TicketSale {
	if perAccountLimit == 0 {
		perAccountLimit = TicketSaleDefaultPerAccountLimit
	}
	return &TicketSale{
		ID:              TicketSaleID(NewID()),
		EventID:         eventID,
		Method:          TicketSaleMethodFirstCome,
		SaleStartTime:   saleStart,
		SaleEndTime:     saleEnd,
		Price:           price,
		Quantity:        quantity,
		PerAccountLimit: perAccountLimit,
		CreateTime:      now,
	}
}

// Validate reports whether the sale's window, price, quantity and per-account
// limit follow the TicketSale rules. The entity layer returns stdlib errors;
// callers wrap them with an apperr code.
func (s *TicketSale) Validate() error {
	if !s.SaleEndTime.After(s.SaleStartTime) {
		return errors.New("sale end must be after the sale start")
	}
	if s.Price < TicketSaleMinPrice || s.Price > TicketSaleMaxPrice {
		return errors.New("price must be 1 to 1,000,000 yen")
	}
	if s.Quantity < 1 {
		return errors.New("quantity must be at least 1")
	}
	if s.PerAccountLimit < 1 || s.PerAccountLimit > TicketSaleMaxPerAccountLimit {
		return errors.New("per-account limit must be 1 to 10")
	}
	return nil
}

// Remaining returns the tickets left to hold: the quantity minus the sold
// count minus the held count, never below 0.
func (s *TicketSale) Remaining() int {
	return max(s.Quantity-s.SoldCount-s.HeldCount, 0)
}

// StateAt returns the sale's state at t, given its sold and held counts.
// Ended takes precedence over SoldOut and AllHeld.
func (s *TicketSale) StateAt(t time.Time) TicketSaleState {
	switch {
	case t.Before(s.SaleStartTime):
		return TicketSaleStateNotYetOnSale
	case !t.Before(s.SaleEndTime):
		return TicketSaleStateEnded
	case s.SoldCount >= s.Quantity:
		return TicketSaleStateSoldOut
	case s.Remaining() == 0:
		return TicketSaleStateAllHeld
	default:
		return TicketSaleStateOnSale
	}
}

// IsLowStockAt reports whether the sale is OnSale at t with at most one tenth
// of its quantity remaining, rounded up — so a sale of 9 is LowStock with 1
// left.
func (s *TicketSale) IsLowStockAt(t time.Time) bool {
	if s.StateAt(t) != TicketSaleStateOnSale {
		return false
	}
	threshold := (s.Quantity + 9) / 10
	return s.Remaining() <= threshold
}

// ValidatePriceChange reports whether the price may change to newPrice: it
// may only while no Reservation has ever been created for the sale. Keeping
// the same price is always allowed.
func (s *TicketSale) ValidatePriceChange(newPrice int64) error {
	if newPrice != s.Price && s.HasReservations {
		return errors.New("the price cannot change once a checkout has started")
	}
	return nil
}

// TicketSaleRepository persists TicketSales. Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type TicketSaleRepository interface {
	// Create stores a new TicketSale with a sold count of 0 and returns it.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the sale breaks the TicketSale rules.
	//  - AlreadyExists: the event already has a TicketSale.
	//  - Internal: database failure.
	Create(ctx context.Context, sale *TicketSale) (*TicketSale, error)

	// Get returns the TicketSale with the given id, with its held count at the
	// given time and whether any Reservation was ever created for it.
	//
	// # Possible errors
	//
	//  - NotFound: no TicketSale has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id TicketSaleID, at time.Time) (*TicketSale, error)

	// GetByEvent returns the event's TicketSale, with its held count at the
	// given time and whether any Reservation was ever created for it.
	//
	// # Possible errors
	//
	//  - NotFound: the event has no TicketSale.
	//  - Internal: database failure.
	GetByEvent(ctx context.Context, eventID string, at time.Time) (*TicketSale, error)

	// Update stores the sale start, sale end, price, quantity and per-account
	// limit of sale.ID, checking against the stored sale in one indivisible step
	// under the sale's row lock, so a Reservation created meanwhile is counted.
	// It returns the updated sale with its counts at the given time.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the result breaks the TicketSale rules.
	//  - FailedPrecondition: the price changes after a Reservation was created,
	//    or the quantity is below the sold count plus the tickets held at that
	//    moment.
	//  - NotFound: no TicketSale has the id.
	//  - Internal: database failure.
	Update(ctx context.Context, sale *TicketSale, at time.Time) (*TicketSale, error)
}
