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
//	Draft --> Entered --> Won | Lost
//	Draft | Entered --> Withdrawn
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

	// LotteryEntryStateWithdrawn means the entry ended before the draw: the fan
	// withdrew it, replaced a Draft with another count, or left a Draft
	// unconfirmed until the draw. Any card authorization is released. A new entry may be
	// started while the window is open.
	LotteryEntryStateWithdrawn LotteryEntryState = 4

	// LotteryEntryStateDraft is the entry being built: it may carry a card authorization
	// but has not been confirmed, takes no part in the draw and is not
	// tallied.
	LotteryEntryStateDraft LotteryEntryState = 5
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
	case LotteryEntryStateWithdrawn:
		return "withdrawn"
	case LotteryEntryStateDraft:
		return "draft"
	default:
		return "UNSPECIFIED"
	}
}

// LotteryEntry is one fan account's entry into the lottery for one
// TicketType: a request for a companion group of tickets allocated
// all-or-nothing, and the card authorization placed for the amount until the draw. The
// fan's 本人確認 (identity check) details are the User's full name and phone
// number.
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
	// CardAuthorization (e.g. a Stripe "pi_..."); empty on a Draft until the
	// card authorization is opened, and set from Entered on.
	CardAuthorizationID string

	// State is the entry's lifecycle state.
	State LotteryEntryState

	// DrawPosition is the entry's zero-based position in its ticket type's
	// random draw order; nil before the draw and set only by the draw. It
	// orders the losers for official resale.
	DrawPosition *int
}

// NewLotteryEntry returns a Draft entry with a generated UUIDv7 id, no
// authorization and no draw position.
func NewLotteryEntry(ticketTypeID TicketTypeID, userID UserID, ticketCount int) *LotteryEntry {
	return &LotteryEntry{
		ID:           LotteryEntryID(NewID()),
		TicketTypeID: ticketTypeID,
		UserID:       userID,
		TicketCount:  ticketCount,
		State:        LotteryEntryStateDraft,
	}
}

// HasCardAuthorization reports whether a CardAuthorization is recorded on the
// entry.
func (e *LotteryEntry) HasCardAuthorization() bool {
	return e.CardAuthorizationID != ""
}

// IsConfirmable reports whether the entry can become Entered: it is a Draft
// with an authorization.
func (e *LotteryEntry) IsConfirmable() bool {
	return e.State == LotteryEntryStateDraft && e.HasCardAuthorization()
}

// IsActive reports whether the entry counts as the account's entry for its
// ticket type: every state except Withdrawn.
func (e *LotteryEntry) IsActive() bool {
	return e.State != LotteryEntryStateWithdrawn
}

// IsWithdrawable reports whether the entry can be withdrawn: it is a Draft or
// Entered, so the draw has not run for it.
func (e *LotteryEntry) IsWithdrawable() bool {
	return e.State == LotteryEntryStateDraft || e.State == LotteryEntryStateEntered
}

// LotteryEntryStats are the counts of one TicketType's Entered, Won and Lost
// entries for the Organizer's view of its sale. Draft and Withdrawn entries
// are not counted.
type LotteryEntryStats struct {
	// EntryCount is the number of Entered, Won and Lost entries.
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
	// GetOrCreateDraft returns the user's Draft for the TicketType when its
	// ticket count equals count, changing nothing. When the user has
	// a Draft with another count, it makes that Draft Withdrawn and creates a
	// new Draft in the same step; with no active entry it creates a new
	// Draft. Checking and creating are one indivisible step, so two concurrent
	// calls leave at most one active entry per user and TicketType.
	//
	// # Possible errors
	//
	//  - AlreadyExists: the user has an Entered, Won or Lost entry for the
	//    TicketType; nothing changed.
	//  - FailedPrecondition: the TicketType does not exist.
	//  - Internal: database failure.
	GetOrCreateDraft(ctx context.Context, ticketTypeID TicketTypeID, userID UserID, count int) (*LotteryEntry, error)

	// SetCardAuthorization records the Draft's CardAuthorization. Recording the
	// same one again succeeds without a change. It changes no other attribute.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the entry is not a Draft, or already has another
	//    CardAuthorization.
	//  - NotFound: no entry has the id.
	//  - Internal: database failure.
	SetCardAuthorization(ctx context.Context, id LotteryEntryID, cardAuthorizationID string) error

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
	// TicketType, in every state, newest first. It returns an empty slice when
	// there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByTicketTypeAndUser(ctx context.Context, ticketTypeID TicketTypeID, userID UserID) ([]*LotteryEntry, error)

	// ListUndrawnForTicketType returns every Draft and Entered entry of the
	// TicketType, in no particular order: the Entered entries the draw draws
	// and the Drafts it withdraws. It returns an empty slice when there is
	// none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListUndrawnForTicketType(ctx context.Context, ticketTypeID TicketTypeID) ([]*LotteryEntry, error)

	// GetStatsByTicketType counts the TicketType's Entered, Won and Lost
	// entries. A TicketType without entries yields all counts zero.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	GetStatsByTicketType(ctx context.Context, ticketTypeID TicketTypeID) (LotteryEntryStats, error)

	// PersistDrawOutcome records one sale's draw in one transaction: every
	// winner Won and every loser Lost with its draw position, every
	// unconfirmed Draft in withdrawnDrafts Withdrawn, across all TicketTypes of
	// the sale, and the sale's draw time. With no winners, losers or Drafts it
	// only sets the draw time. On failure nothing is recorded.
	//
	// # Possible errors
	//
	//  - InvalidArgument: saleID is empty.
	//  - Internal: database failure.
	PersistDrawOutcome(ctx context.Context, saleID TicketSaleID, winners []DrawWinnerRow, losers []DrawLoserRow, withdrawnDrafts []LotteryEntryID) error

	// UpdateState sets the entry's state; it changes no other attribute.
	//
	// # Possible errors
	//
	//  - NotFound: no entry has the id.
	//  - Internal: database failure.
	UpdateState(ctx context.Context, id LotteryEntryID, state LotteryEntryState) error
}
