package entity

import (
	"context"
	"errors"
	"slices"
	"time"
	"unicode/utf8"
)

// LotteryDrawDelay is how long after a Lottery sale's end time its draw
// starts. A lottery entry is stored only before the end time by the database
// clock, in one transaction whose waits are bounded to 10 seconds, so a draw
// that starts this long after the end time reads every entry made in time.
const LotteryDrawDelay = time.Minute

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

// TicketSale (受付) is one sale opportunity of a Series: its name, method and
// window. It is of one of two kinds, told apart by the Series' Organizer and
// mirrored on the sale by DiscoveredTime:
//
//   - An Organizer's sale (Series with an Organizer, DiscoveredTime nil) is
//     sold on the platform. It has a verification requirement and names the
//     events it sells; it sells every [TicketStock] of those events.
//   - A discovered sale (Series without an Organizer, DiscoveredTime set) was
//     found on the artist's official pages and is only reported to fans. It
//     names no event.
//
// Mirrors liverty_music.entity.v1.TicketSale, which carries an Organizer's
// sale only.
type TicketSale struct {
	// ID is the surrogate primary key (UUIDv7).
	ID TicketSaleID
	// SeriesID is the Series whose events the sale covers.
	SeriesID string
	// Name is the label fans and members see (e.g. "ファンクラブ先行"), 1 to 100
	// characters, on every sale.
	Name string
	// Method is how tickets are allocated (FirstCome or Lottery).
	Method TicketSaleMethod
	// SaleStartTime is when the window opens.
	SaleStartTime time.Time
	// SaleEndTime is when the window closes: after SaleStartTime, and on an
	// Organizer's sale no later than the start of every event the sale covers
	// (checked by the usecase, which reads the events).
	SaleEndTime time.Time
	// VerificationRequirement says whether a fan must hold an Active
	// IdentityVerification. Always None on a discovered sale.
	VerificationRequirement VerificationRequirement
	// DiscoveredTime is when discovery first stored a discovered sale; nil on
	// an Organizer's sale. It never changes once set.
	DiscoveredTime *time.Time
	// EventIDs are the events an Organizer's sale sells, at least one, each an
	// event of its Series. A discovered sale names none.
	EventIDs []string
}

// NewTicketSale returns an Organizer's sale of the events with a generated
// UUIDv7 id and the verification requirement None.
func NewTicketSale(seriesID, name string, method TicketSaleMethod, saleStart, saleEnd time.Time, eventIDs ...string) *TicketSale {
	return &TicketSale{
		ID:            TicketSaleID(NewID()),
		SeriesID:      seriesID,
		Name:          name,
		Method:        method,
		SaleStartTime: saleStart,
		SaleEndTime:   saleEnd,
		EventIDs:      eventIDs,
	}
}

// IsDiscovered reports whether the sale is a discovered sale rather than an
// Organizer's sale.
func (s *TicketSale) IsDiscovered() bool {
	return s.DiscoveredTime != nil
}

// Validate reports whether the sale follows the TicketSale rules for its kind:
// a name, a method, a window with an end, and for an Organizer's sale at least
// one event, none named twice, or for a discovered sale no event and the
// verification requirement None. That every named event is one of the
// Series' events is checked with [TicketSale.CheckEventsOfSeries]. The entity
// layer returns stdlib errors; callers wrap them with an apperr code.
func (s *TicketSale) Validate() error {
	if !s.Method.IsValid() {
		return errors.New("sale method must be FirstCome or Lottery")
	}
	if s.SaleStartTime.IsZero() {
		return errors.New("sale start time is required")
	}
	if err := s.ValidateWindow(s.SaleStartTime, s.SaleEndTime); err != nil {
		return err
	}
	if n := utf8.RuneCountInString(s.Name); n < 1 || n > TicketSaleNameMaxLen {
		return errors.New("sale name must be 1 to 100 characters")
	}
	if s.IsDiscovered() {
		if len(s.EventIDs) > 0 {
			return errors.New("a discovered sale names no event")
		}
		if s.VerificationRequirement != VerificationRequirementNone {
			return errors.New("a discovered sale has no verification requirement")
		}
		return nil
	}
	if len(s.EventIDs) == 0 {
		return errors.New("an Organizer's sale names at least one event")
	}
	events := make(map[string]struct{}, len(s.EventIDs))
	for _, id := range s.EventIDs {
		if _, dup := events[id]; dup {
			return errors.New("a sale names each event at most once")
		}
		events[id] = struct{}{}
	}
	return nil
}

// CheckEventsOfSeries reports whether every event the sale names is among
// events and belongs to the sale's Series. The entity layer returns stdlib
// errors; callers wrap them with an apperr code.
func (s *TicketSale) CheckEventsOfSeries(events []*Event) error {
	series := make(map[string]string, len(events))
	for _, e := range events {
		series[e.ID] = e.SeriesID
	}
	for _, id := range s.EventIDs {
		if got, ok := series[id]; !ok || got != s.SeriesID {
			return errors.New("every event of a sale must be an event of its Series")
		}
	}
	return nil
}

// ValidateWindow reports whether start and end form a valid window for the
// sale's method and kind: the end is set and after the start, and an
// Organizer's Lottery window lasts 1 to 14 days. It does not read the sale's
// stored window, so a usecase can check a new window before storing it.
func (s *TicketSale) ValidateWindow(start, end time.Time) error {
	if end.IsZero() {
		return errors.New("a sale needs an end time")
	}
	if !end.After(start) {
		return errors.New("sale end must be after the sale start")
	}
	if s.Method == TicketSaleMethodLottery && !s.IsDiscovered() {
		if d := end.Sub(start); d < LotteryMinWindow || d > LotteryMaxWindow {
			return errors.New("a lottery window must last 1 to 14 days")
		}
	}
	return nil
}

// IsNotYetOverAt reports whether the sale is not yet over at t: t is before
// its end time.
func (s *TicketSale) IsNotYetOverAt(t time.Time) bool {
	return t.Before(s.SaleEndTime)
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

// IsDrawnAt reports whether an Organizer's Lottery sale counts as drawn at t,
// given how many of its LotteryEntries are Entered: t is at or after its end
// time and none is Entered. A closed sale with no entries counts as drawn; a
// discovered sale is never drawn.
func (s *TicketSale) IsDrawnAt(t time.Time, enteredEntries int) bool {
	return s.Method == TicketSaleMethodLottery && !s.IsDiscovered() && !t.Before(s.SaleEndTime) && enteredEntries == 0
}

// NamesEvent reports whether the sale names the event.
func (s *TicketSale) NamesEvent(eventID string) bool {
	return slices.Contains(s.EventIDs, eventID)
}

// TicketSaleRepository persists TicketSales and the events they name.
// Implementations live in internal/infrastructure/database/rdb/.
//
// Every read that returns an Organizer's sale returns the events it names.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type TicketSaleRepository interface {
	// Create stores the sale together with the events it names, or nothing,
	// and returns the sale with its events.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the sale breaks the TicketSale rules or names the
	//    same event twice.
	//  - FailedPrecondition: the Series or a named event does not exist.
	//  - Internal: database failure.
	Create(ctx context.Context, sale *TicketSale) (*TicketSale, error)

	// Get returns the TicketSale with the given id with the events it names.
	//
	// # Possible errors
	//
	//  - NotFound: no TicketSale has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id TicketSaleID) (*TicketSale, error)

	// ListByEvent returns every sale that names the event, each with the
	// events it names, ordered by start time. It returns an empty slice when
	// no sale names the event. The event's stocks are read with
	// [TicketStockRepository.ListByEvent].
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByEvent(ctx context.Context, eventID string) ([]*TicketSale, error)

	// ListDueForDraw returns every Organizer's Lottery sale whose end time is
	// at least [LotteryDrawDelay] before now and that still has an Entered
	// LotteryEntry, each with its events. A discovered sale is never returned:
	// the platform takes no entries for it. It returns an empty slice when
	// none is due.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListDueForDraw(ctx context.Context, now time.Time) ([]*TicketSale, error)

	// UpsertDiscovered stores a discovered sale. The known discovered sale with
	// the same series and method and a start time on the same calendar day in
	// Japan time (Asia/Tokyo) is the same sale: its name, start and end times
	// are replaced with the discovered ones, its id, series, method and
	// discovered time are kept, and UpsertOutcomeUpdated is returned with its
	// id. Otherwise a new discovered sale with a new id and the current time as
	// discovered time is created, and UpsertOutcomeInserted is returned with
	// the new id.
	//
	// A discovered sale with a zero start or end time is skipped: ("",
	// UpsertOutcomeSkipped, nil), nothing written. It never removes a sale.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the sale names no series, has no name or breaks the
	//    TicketSale rules for a discovered sale.
	//  - FailedPrecondition: the series does not exist or has an Organizer.
	//  - Internal: database failure.
	//
	// # Announces
	//
	// SALES_PHASE.discovered with the TicketSale, together with the insert, when it
	// creates the sale. Nothing when it updates or skips one.
	UpsertDiscovered(ctx context.Context, sale *DiscoveredTicketSale) (TicketSaleID, UpsertOutcome, error)

	// ListBySeries returns every sale of the series, of either kind, each with
	// its events, ordered by start time, earliest first. A series with no
	// sale, or one that does not exist, returns an empty slice.
	//
	// # Possible errors
	//
	//  - InvalidArgument: seriesID is empty.
	//  - Internal: database failure.
	ListBySeries(ctx context.Context, seriesID string) ([]*TicketSale, error)

	// ListWithPendingMilestones returns every sale, discovered or an
	// Organizer's, whose start time is no later than now plus lookahead and
	// whose end time is no earlier than now minus lookback, ordered by start
	// time, each with its events. An Organizer's sale is returned only while
	// its Series is Published (at least one of its events is PUBLISHED).
	//
	// # Possible errors
	//
	//  - InvalidArgument: lookahead is not positive or lookback is negative.
	//  - Internal: database failure.
	ListWithPendingMilestones(ctx context.Context, lookahead, lookback time.Duration) ([]*TicketSale, error)

	// SetWindow stores saleStart and saleEnd as the sale's window, leaves its
	// other attributes and its events unchanged, and returns the updated sale
	// with its events.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the window breaks the TicketSale rules.
	//  - NotFound: no TicketSale has the id.
	//  - Internal: database failure.
	SetWindow(ctx context.Context, id TicketSaleID, saleStart, saleEnd time.Time) (*TicketSale, error)

	// SetVerificationRequirement sets the sale's verification requirement,
	// leaves its other attributes and its events unchanged, and returns the
	// updated sale.
	//
	// # Possible errors
	//
	//  - InvalidArgument: id is empty.
	//  - NotFound: no TicketSale has the id.
	//  - Internal: database failure.
	SetVerificationRequirement(ctx context.Context, id TicketSaleID, req VerificationRequirement) (*TicketSale, error)
}
