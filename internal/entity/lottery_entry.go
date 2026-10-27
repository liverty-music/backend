package entity

import (
	"context"
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

// LotteryEntry is one fan account's entry into the lottery for one
// TicketType: a request for a companion group of tickets allocated
// all-or-nothing, made when the fan confirms an authorized card authorization
// for the amount, which holds the amount until the draw. The fan's 本人確認
// (identity check) details are the User's full name and phone number.
//
// Mirrors liverty_music.entity.v1.LotteryEntry.
type LotteryEntry struct {
	// ID is the surrogate primary key (UUIDv7).
	ID LotteryEntryID

	// TicketTypeID is the TicketType entered; its sale's method is Lottery.
	TicketTypeID TicketTypeID

	// UserID is the fan account that entered.
	UserID UserID

	// TicketCount is the companion-group size, allocated
	// all-or-nothing: 1 to the ticket type's per-account limit.
	TicketCount int

	// CardAuthorizationID is the payment provider's ID for the entry's
	// CardAuthorization (e.g. a Stripe "pi_..."); set at creation. One card
	// authorization makes at most one entry.
	CardAuthorizationID string

	// State is the entry's lifecycle state.
	State LotteryEntryState

	// DrawPosition is the entry's zero-based position in its ticket type's
	// random draw order; nil before the draw and set only by the draw. It
	// orders the losers for official resale.
	DrawPosition *int

	// ReferralCode is the code of the link the fan came from, carried by the
	// card authorization the entry was made from; nil without one. Set at
	// creation and never changed.
	ReferralCode *ReferralCode
}

// NewLotteryEntry returns an Entered entry with a generated UUIDv7 id, the
// card authorization it is made from, the optional referral code and no draw
// position.
func NewLotteryEntry(ticketTypeID TicketTypeID, userID UserID, ticketCount int, cardAuthorizationID string, code *ReferralCode) *LotteryEntry {
	return &LotteryEntry{
		ID:                  LotteryEntryID(NewID()),
		TicketTypeID:        ticketTypeID,
		UserID:              userID,
		TicketCount:         ticketCount,
		CardAuthorizationID: cardAuthorizationID,
		State:               LotteryEntryStateEntered,
		ReferralCode:        code,
	}
}

// LotteryEntryStats are the counts of one TicketType's entries for the
// Organizer's view of its sale.
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

	// CardAuthorizationID is the entry's CardAuthorization, which the draw
	// captures.
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

	// CardAuthorizationID is the entry's CardAuthorization, which the draw
	// releases.
	CardAuthorizationID string

	// DrawPosition is the entry's zero-based position in the draw order; it
	// keeps the order of the losers for official resale.
	DrawPosition int
}

// LotteryEntryRepository persists [LotteryEntry] records. Implementations live
// in internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type LotteryEntryRepository interface {
	// Create stores the new Entered entry and returns it. When an entry
	// already holds the entry's card authorization, it stores nothing and
	// returns that entry, also after the sale's end time. Checking and
	// storing are one indivisible step: two concurrent calls for the same
	// user and TicketType, or for the same card authorization, store one
	// entry, and no entry is stored once the sale's end time has passed by
	// the database clock.
	//
	// # Possible errors
	//
	//  - AlreadyExists: the user already has an entry for the TicketType;
	//    nothing is stored.
	//  - FailedPrecondition: the TicketType does not exist, or its sale's end
	//    time has passed; nothing is stored.
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

	// GetByCardAuthorizationID returns the entry whose card authorization has the given
	// reference.
	//
	// # Possible errors
	//
	//  - NotFound: no entry has the reference.
	//  - Internal: database failure.
	GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*LotteryEntry, error)

	// ListByTicketTypeAndUser returns every entry of the user for the
	// TicketType, in every state, newest first: at most one. It returns an
	// empty slice when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByTicketTypeAndUser(ctx context.Context, ticketTypeID TicketTypeID, userID UserID) ([]*LotteryEntry, error)

	// ListByUser returns every entry of the user, for every TicketType and in
	// every state, newest first. It returns an empty slice when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByUser(ctx context.Context, userID UserID) ([]*LotteryEntry, error)

	// ListUndrawnForTicketType returns every Entered entry of the TicketType,
	// in no particular order: the entries the draw draws. It returns an empty
	// slice when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListUndrawnForTicketType(ctx context.Context, ticketTypeID TicketTypeID) ([]*LotteryEntry, error)

	// GetStatsByTicketType counts the TicketType's entries. A TicketType
	// without entries yields all counts zero.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	GetStatsByTicketType(ctx context.Context, ticketTypeID TicketTypeID) (LotteryEntryStats, error)

	// PersistDrawOutcome records one sale's draw in one transaction: every
	// winner Won and every loser Lost with its draw position, across all
	// TicketTypes of the sale, and the sale's draw time. With no winners and
	// no losers it only sets the draw time. On failure nothing is recorded.
	//
	// # Possible errors
	//
	//  - InvalidArgument: saleID is empty.
	//  - NotFound: the sale does not exist.
	//  - Internal: database failure.
	PersistDrawOutcome(ctx context.Context, saleID TicketSaleID, winners []DrawWinnerRow, losers []DrawLoserRow) error
}
