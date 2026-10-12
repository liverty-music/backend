package entity

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
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
	// TicketSaleMethodLottery is a ballot: fans enter during the window, and a
	// random draw after it closes decides the winners.
	TicketSaleMethodLottery TicketSaleMethod = 2
)

// IsValid reports whether m is FirstCome or Lottery.
func (m TicketSaleMethod) IsValid() bool {
	return m == TicketSaleMethodFirstCome || m == TicketSaleMethodLottery
}

// VerificationRequirement states whether and how a TicketSale requires a fan
// to hold an Active [IdentityVerification]. Values mirror the proto enum
// liverty_music.entity.v1.VerificationRequirement.
//
// MVP is JPKI-only: no driver's-licence fallback ships in this iteration, so
// both [VerificationRequirementVerifiedAny] and [VerificationRequirementJPKIOnly]
// behave identically — they require an Active [IdentityVerification] established via JPKI. The
// distinction is preserved for when the licence fallback is introduced.
type VerificationRequirement int8

const (
	// VerificationRequirementNone means the sale has no verification
	// requirement. Any signed-in fan may take part.
	VerificationRequirementNone VerificationRequirement = 0

	// VerificationRequirementVerifiedAny means the fan must hold an Active IdentityVerification
	// (JPKI or, in the future, the driver's-licence fallback).
	VerificationRequirementVerifiedAny VerificationRequirement = 1

	// VerificationRequirementJPKIOnly means the fan must hold an Active
	// JPKI-backed [IdentityVerification]. No licence fallback is offered.
	VerificationRequirementJPKIOnly VerificationRequirement = 2
)

// RequiresVerification reports whether the requirement demands an Active
// [IdentityVerification]: every value except None does.
func (r VerificationRequirement) RequiresVerification() bool {
	return r != VerificationRequirementNone
}

const (
	// TicketSaleNameMaxLen is the longest sale name accepted, in characters.
	TicketSaleNameMaxLen = 100
	// LotteryMinWindow is the shortest window of a Lottery sale.
	LotteryMinWindow = 24 * time.Hour
	// LotteryMaxWindow is the longest window of a Lottery sale.
	LotteryMaxWindow = 14 * 24 * time.Hour
)

// TicketSale (受付) is one sale opportunity of a Series: its name, method,
// window, verification requirement and, for a Lottery, when it was drawn. What
// it offers for each event is a [TicketType]; a sale has at most one
// TicketType per event.
//
// Mirrors liverty_music.entity.v1.TicketSale.
type TicketSale struct {
	// ID is the surrogate primary key (UUIDv7).
	ID TicketSaleID
	// SeriesID is the Series whose events the sale covers.
	SeriesID string
	// Name tells the sale apart from the event's other sales (e.g.
	// "ファンクラブ先行"), 1 to 100 characters.
	Name string
	// Method is how tickets are allocated (FirstCome or Lottery).
	Method TicketSaleMethod
	// SaleStartTime is when the window opens.
	SaleStartTime time.Time
	// SaleEndTime is when the window closes: after SaleStartTime, and before
	// the start of every event the sale covers (checked by the usecase, which
	// reads the events).
	SaleEndTime time.Time
	// VerificationRequirement says whether a fan must hold an Active IdentityVerification.
	VerificationRequirement VerificationRequirement
	// DrawTime is when a Lottery sale was drawn; nil until drawn and always
	// nil for a FirstCome sale.
	DrawTime *time.Time
	// TicketTypes are what the sale offers, one per event.
	TicketTypes []*TicketType
}

// NewTicketSale returns a sale with a generated UUIDv7 id, the verification
// requirement None, no draw time and no ticket types. Add ticket types with
// [NewTicketType].
func NewTicketSale(seriesID, name string, method TicketSaleMethod, saleStart, saleEnd time.Time) *TicketSale {
	return &TicketSale{
		ID:            TicketSaleID(NewID()),
		SeriesID:      seriesID,
		Name:          name,
		Method:        method,
		SaleStartTime: saleStart,
		SaleEndTime:   saleEnd,
	}
}

// Validate reports whether the sale follows the TicketSale rules — its name,
// method and window — and whether each of its ticket types follows the
// TicketType rules, with at most one ticket type per event. The entity layer
// returns stdlib errors; callers wrap them with an apperr code.
func (s *TicketSale) Validate() error {
	if n := utf8.RuneCountInString(s.Name); n < 1 || n > TicketSaleNameMaxLen {
		return errors.New("sale name must be 1 to 100 characters")
	}
	if !s.Method.IsValid() {
		return errors.New("sale method must be FirstCome or Lottery")
	}
	if err := s.ValidateWindow(s.SaleStartTime, s.SaleEndTime); err != nil {
		return err
	}
	events := make(map[string]struct{}, len(s.TicketTypes))
	for _, tt := range s.TicketTypes {
		if err := tt.Validate(); err != nil {
			return err
		}
		if _, dup := events[tt.EventID]; dup {
			return errors.New("a sale has at most one ticket type per event")
		}
		events[tt.EventID] = struct{}{}
	}
	return nil
}

// ValidateWindow reports whether start and end form a valid window for the
// sale's method: the end is after the start, and a Lottery window lasts 1 to
// 14 days. It does not read the sale's stored window, so a usecase can check a
// new window before storing it.
func (s *TicketSale) ValidateWindow(start, end time.Time) error {
	if !end.After(start) {
		return errors.New("sale end must be after the sale start")
	}
	if s.Method == TicketSaleMethodLottery {
		if d := end.Sub(start); d < LotteryMinWindow || d > LotteryMaxWindow {
			return errors.New("a lottery window must last 1 to 14 days")
		}
	}
	return nil
}

// IsOpenAt reports whether the window is open at t: at or after the start
// and before the end.
func (s *TicketSale) IsOpenAt(t time.Time) bool {
	return !t.Before(s.SaleStartTime) && t.Before(s.SaleEndTime)
}

// Overlaps reports whether the two sales' windows overlap: each starts
// before the other ends. Back-to-back windows do not overlap.
func (s *TicketSale) Overlaps(other *TicketSale) bool {
	return s.SaleStartTime.Before(other.SaleEndTime) && other.SaleStartTime.Before(s.SaleEndTime)
}

// IsDrawn reports whether the sale's draw time is set.
func (s *TicketSale) IsDrawn() bool {
	return s.DrawTime != nil
}

// TicketType returns the sale's ticket type with the given id, or nil when
// the sale has none with that id.
func (s *TicketSale) TicketType(id TicketTypeID) *TicketType {
	for _, tt := range s.TicketTypes {
		if tt.ID == id {
			return tt
		}
	}
	return nil
}

// TicketSaleRepository persists TicketSales and their TicketTypes. A
// TicketType is always read with its sale, so its operations live here too.
// Implementations live in internal/infrastructure/database/rdb/.
//
// Every read that returns a sale returns all of its TicketTypes; each
// TicketType of a FirstCome sale carries its held count at the given time.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type TicketSaleRepository interface {
	// Create stores the sale without a draw time and all of its TicketTypes
	// together, or nothing, and returns the sale with its TicketTypes. Each
	// TicketType of a FirstCome sale starts with a sold count of 0.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the sale or a TicketType breaks the TicketSale or
	//    TicketType rules, or two TicketTypes are for the same event.
	//  - FailedPrecondition: the Series or an event of a TicketType does not
	//    exist.
	//  - Internal: database failure.
	Create(ctx context.Context, sale *TicketSale) (*TicketSale, error)

	// Get returns the TicketSale with the given id with all of its
	// TicketTypes.
	//
	// # Possible errors
	//
	//  - NotFound: no TicketSale has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id TicketSaleID, at time.Time) (*TicketSale, error)

	// ListByEvent returns every sale that has a TicketType for the event, each
	// with all of its TicketTypes, ordered by start time. It returns an empty
	// slice when the event has none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByEvent(ctx context.Context, eventID string, at time.Time) ([]*TicketSale, error)

	// ListDueForDraw returns every Lottery sale whose end time is at or before
	// now and that is not drawn, each with all of its TicketTypes. It returns
	// an empty slice when none is due.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListDueForDraw(ctx context.Context, now time.Time) ([]*TicketSale, error)

	// SetWindow stores saleStart and saleEnd as the sale's window, leaves its
	// other attributes and its TicketTypes unchanged, and returns the updated
	// sale with its TicketTypes.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the window breaks the TicketSale rules.
	//  - NotFound: no TicketSale has the id.
	//  - Internal: database failure.
	SetWindow(ctx context.Context, id TicketSaleID, saleStart, saleEnd time.Time, at time.Time) (*TicketSale, error)

	// SetVerificationRequirement sets the sale's verification requirement,
	// leaves its other attributes and its TicketTypes unchanged, and returns
	// the updated sale with its TicketTypes.
	//
	// # Possible errors
	//
	//  - InvalidArgument: id is empty.
	//  - NotFound: no TicketSale has the id.
	//  - Internal: database failure.
	SetVerificationRequirement(ctx context.Context, id TicketSaleID, req VerificationRequirement, at time.Time) (*TicketSale, error)

	// GetTicketType returns the sale that offers the TicketType with the given
	// id, with all of its TicketTypes as Get returns them, and that TicketType
	// (the element of sale.TicketTypes with the id).
	//
	// # Possible errors
	//
	//  - NotFound: no TicketType has the id.
	//  - Internal: database failure.
	GetTicketType(ctx context.Context, id TicketTypeID, at time.Time) (*TicketSale, *TicketType, error)

	// UpdateTicketType stores the price, quantity and per-account limit of
	// ticketType.ID and returns its sale, as GetTicketType does, with the
	// updated TicketType. For a TicketType of a FirstCome sale the check
	// against the stored sold and held counts and the change are one
	// indivisible step, so a Checkout created meanwhile is counted.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the result breaks the TicketType rules.
	//  - FailedPrecondition: for a FirstCome TicketType, the price changes
	//    while any ticket is sold or held, or the quantity is below the sold
	//    count plus the tickets held at that moment.
	//  - NotFound: no TicketType has the id.
	//  - Internal: database failure.
	UpdateTicketType(ctx context.Context, ticketType *TicketType, at time.Time) (*TicketSale, *TicketType, error)
}
