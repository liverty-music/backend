package entity

import (
	"context"
	"errors"
)

// LotteryEntryID is the opaque identifier for a single LotteryEntry.
//
// Mirrors liverty_music.entity.v1.LotteryEntryId.value (a UUID string).
type LotteryEntryID string

// LotteryEntryState is the lifecycle state of a [LotteryEntry]. Values mirror
// the proto enum liverty_music.entity.v1.LotteryEntryState and the stored
// state.
//
//	[*] --> Entered --> Won | Lost
type LotteryEntryState int8

const (
	// LotteryEntryStateUnspecified is the zero value and is never persisted.
	LotteryEntryStateUnspecified LotteryEntryState = 0

	// LotteryEntryStateEntered means the fan confirmed the entry with its card
	// authorization in place; it takes part in the draw.
	LotteryEntryStateEntered LotteryEntryState = 1

	// LotteryEntryStateWon means the draw allocated tickets to the entry. Its
	// card authorization is captured.
	LotteryEntryStateWon LotteryEntryState = 2

	// LotteryEntryStateLost means the draw did not allocate tickets to the
	// entry. Its card authorization is released.
	LotteryEntryStateLost LotteryEntryState = 3
)

// String returns the lowercase state name.
func (s LotteryEntryState) String() string {
	switch s {
	case LotteryEntryStateEntered:
		return "entered"
	case LotteryEntryStateWon:
		return "won"
	case LotteryEntryStateLost:
		return "lost"
	default:
		return "UNSPECIFIED"
	}
}

// LotteryEntry is how one fan's Order takes part in a Lottery TicketSale: a
// request for a companion group of tickets of the Order's TicketStock,
// allocated all-or-nothing, made when the fan confirms an authorized card
// authorization for the Order's amount, which holds the amount until the
// draw. A fan enters a sale at most once for each event it names. The fan,
// the TicketStock, the ticket count, the card authorization and the referral
// code are the Order's; the fan's 本人確認 (identity check) details are the
// User's full name and phone number.
//
// Mirrors liverty_music.entity.v1.LotteryEntry.
type LotteryEntry struct {
	// ID is the surrogate primary key (UUIDv7).
	ID LotteryEntryID

	// OrderID is the Order the entry applies for; one entry per Order.
	OrderID OrderID

	// TicketSaleID is the Lottery TicketSale entered; it names the event of
	// the Order's TicketStock.
	TicketSaleID TicketSaleID

	// State is the entry's lifecycle state.
	State LotteryEntryState

	// DrawPosition is the entry's zero-based position in its sale's random
	// draw order for its TicketStock; nil before the draw and set only by the
	// draw. It orders the losers for official resale.
	DrawPosition *int

	// Order is the Order the entry applies for, read with every entry.
	Order *Order
}

// NewLotteryEntry returns an Entered entry of the sale for order, with a
// generated UUIDv7 id and no draw position, and names it as order's source.
// order carries the card authorization the entry is made from.
func NewLotteryEntry(order *Order, saleID TicketSaleID) *LotteryEntry {
	e := &LotteryEntry{
		ID:           LotteryEntryID(NewID()),
		OrderID:      order.ID,
		TicketSaleID: saleID,
		State:        LotteryEntryStateEntered,
		Order:        order,
	}
	order.LotteryEntryID = e.ID
	return e
}

// ValidateLotteryEntrySale reports whether an entry can belong to sale for
// stock: a Lottery sale that names the stock's event. The entity layer
// returns stdlib errors; callers wrap them with an apperr code.
func ValidateLotteryEntrySale(sale *TicketSale, stock *TicketStock) error {
	if sale == nil {
		return errors.New("a lottery entry belongs to a ticket sale")
	}
	if sale.Method != TicketSaleMethodLottery {
		return errors.New("a lottery entry belongs to a lottery sale")
	}
	if !sale.NamesEvent(stock.EventID) {
		return errors.New("a lottery entry's sale must name the event of its ticket stock")
	}
	return nil
}

// LotteryEntryStats are the counts of one sale's entries for one TicketStock,
// for the Organizer's view of the sale.
type LotteryEntryStats struct {
	// EntryCount is the number of entries.
	EntryCount int

	// TicketCount is the sum of those entries' requested ticket
	// counts.
	TicketCount int

	// WonEntryCount is the number of Won entries.
	WonEntryCount int

	// WonTicketCount is the sum of the Won entries' ticket counts.
	WonTicketCount int

	// LostEntryCount is the number of Lost entries.
	LostEntryCount int
}

// DrawWinnerRow carries what persisting one winning draw outcome needs.
// Built from [RunLotteryDraw]'s result and consumed by
// [LotteryEntryRepository.PersistDrawOutcome].
type DrawWinnerRow struct {
	// LotteryEntryID is the winning entry.
	LotteryEntryID LotteryEntryID

	// CardAuthorizationID is the card authorization of the entry's Order,
	// which the draw captures.
	CardAuthorizationID string

	// DrawPosition is the entry's zero-based position in the draw order.
	DrawPosition int
}

// DrawLoserRow carries what persisting one losing draw outcome needs. Built
// from [RunLotteryDraw]'s result and consumed by
// [LotteryEntryRepository.PersistDrawOutcome].
type DrawLoserRow struct {
	// LotteryEntryID is the losing entry.
	LotteryEntryID LotteryEntryID

	// CardAuthorizationID is the card authorization of the entry's Order,
	// which the draw releases.
	CardAuthorizationID string

	// DrawPosition is the entry's zero-based position in the draw order; it
	// keeps the order of the losers for official resale.
	DrawPosition int
}

// LotteryEntryRepository persists [LotteryEntry] records together with their
// Orders. Every read returns the entry with its Order. Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type LotteryEntryRepository interface {
	// Create stores the new Entered entry together with its Pending Order
	// (entry.Order, which carries the card authorization) and returns them.
	// When an Order already holds the card authorization, it stores nothing
	// and returns that Order's entry, also after the sale's end time. It locks
	// the user's users row first, so checking and storing are one indivisible
	// step with every other creation of an Order by the user, and no entry is
	// stored once the sale's end time has passed by the database clock.
	//
	// # Possible errors
	//
	//  - AlreadyExists: the user already has an entry in the sale for the
	//    stock's event, of any stock of that event; nothing is stored.
	//  - FailedPrecondition: the application does not fit the stock's event
	//    ([Event.Fits] with [OrderRepository.GetHeldForEvent]), the sale or
	//    the stock does not exist, or the sale's end time has passed; nothing
	//    is stored.
	//  - NotFound: the user does not exist, also when it was deleted while
	//    the call waited; nothing is stored.
	//  - Unavailable: the wait for a concurrent call or a lock timed out.
	//  - Internal: database failure.
	Create(ctx context.Context, entry *LotteryEntry) (*LotteryEntry, error)

	// Get returns the entry with the given id, in any state.
	//
	// # Possible errors
	//
	//  - NotFound: no entry has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id LotteryEntryID) (*LotteryEntry, error)

	// GetByCardAuthorizationID returns the entry whose Order's card
	// authorization has the given id.
	//
	// # Possible errors
	//
	//  - NotFound: there is none.
	//  - Internal: database failure.
	GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*LotteryEntry, error)

	// ListByTicketTypeAndUser returns the user's entries in the sale, at most
	// one for each event the sale names, in every state, newest first. It
	// returns an empty slice when there is none. The name is kept until the
	// rename that follows remodel-ticket-sales (design D11).
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByTicketTypeAndUser(ctx context.Context, saleID TicketSaleID, userID UserID) ([]*LotteryEntry, error)

	// ListByUser returns every entry whose Order's user is the user, for every
	// sale and in every state, newest first. It returns an empty slice when
	// there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByUser(ctx context.Context, userID UserID) ([]*LotteryEntry, error)

	// ListUndrawnForTicketType returns every Entered entry of the sale whose
	// Order is of the stock, in no particular order: the entries the draw of
	// the stock draws. It returns an empty slice when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListUndrawnForTicketType(ctx context.Context, saleID TicketSaleID, stockID TicketStockID) ([]*LotteryEntry, error)

	// GetStatsByTicketType counts the sale's entries whose Order is of the
	// stock. A stock without entries in the sale yields all counts zero.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	GetStatsByTicketType(ctx context.Context, saleID TicketSaleID, stockID TicketStockID) (LotteryEntryStats, error)

	// PersistDrawOutcome records one sale's draw in one transaction, under the
	// row lock of every TicketStock it draws, taken in id order: every winner
	// Won and every loser Lost with its draw position and its Order Void,
	// across all TicketStocks of the sale's events. A given entry that is no
	// longer Entered is left unchanged, so recording the same outcome again
	// changes nothing. On failure nothing is recorded.
	//
	// # Possible errors
	//
	//  - InvalidArgument: saleID is empty.
	//  - NotFound: the sale does not exist.
	//  - Internal: database failure.
	PersistDrawOutcome(ctx context.Context, saleID TicketSaleID, winners []DrawWinnerRow, losers []DrawLoserRow) error
}
