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
	// hold in place; it takes part in the draw.
	LotteryEntryStateEntered LotteryEntryState = 1

	// LotteryEntryStateWon means the draw allocated tickets to the entry. Its
	// hold is captured.
	LotteryEntryStateWon LotteryEntryState = 2

	// LotteryEntryStateLost means the draw did not allocate tickets to the
	// entry. Its hold is released.
	LotteryEntryStateLost LotteryEntryState = 3

	// LotteryEntryStateWithdrawn means the entry ended before the draw: the fan
	// withdrew it, replaced a Draft with another count, or left a Draft
	// unconfirmed until the draw. Any hold is released. A new entry may be
	// started while the window is open.
	LotteryEntryStateWithdrawn LotteryEntryState = 4

	// LotteryEntryStateDraft is the entry being built: it may carry a card hold
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

// PaymentAuthorization is the reference to the card hold (a manual-capture
// Stripe PaymentIntent) placed for a lottery entry's amount. No money is
// captured until the draw runs.
//
// Mirrors liverty_music.entity.v1.PaymentAuthorization.
type PaymentAuthorization struct {
	// PaymentIntentRef is the Stripe PaymentIntent ID (e.g. "pi_3Qx..."),
	// 1 to 255 characters; empty while no hold is opened.
	PaymentIntentRef string
}

// LotteryEntry is one fan account's entry into the lottery for one
// TicketType: a request for a companion group of tickets allocated
// all-or-nothing, and the card hold placed for the amount until the draw. The
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

	// RequestedTicketCount is the companion-group size, allocated
	// all-or-nothing: 1 to the ticket type's per-account limit.
	RequestedTicketCount int

	// Authorization is the card hold; its reference is empty on a Draft until
	// the hold is opened, and set from Entered on.
	Authorization PaymentAuthorization

	// State is the entry's lifecycle state.
	State LotteryEntryState

	// DrawPosition is the entry's zero-based position in its ticket type's
	// random draw order; nil before the draw and set only by the draw. It
	// orders the loser waitlist for official resale.
	DrawPosition *int
}

// NewLotteryEntry returns a Draft entry with a generated UUIDv7 id, no
// authorization and no draw position.
func NewLotteryEntry(ticketTypeID TicketTypeID, userID UserID, requestedTicketCount int) *LotteryEntry {
	return &LotteryEntry{
		ID:                   LotteryEntryID(NewID()),
		TicketTypeID:         ticketTypeID,
		UserID:               userID,
		RequestedTicketCount: requestedTicketCount,
		State:                LotteryEntryStateDraft,
	}
}

// HasAuthorization reports whether a card hold is recorded on the entry.
func (e *LotteryEntry) HasAuthorization() bool {
	return e.Authorization.PaymentIntentRef != ""
}

// IsConfirmable reports whether the entry can become Entered: it is a Draft
// with an authorization.
func (e *LotteryEntry) IsConfirmable() bool {
	return e.State == LotteryEntryStateDraft && e.HasAuthorization()
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

// TicketTypeStats is the tally of one TicketType's Entered, Won and Lost
// entries for the Organizer's view of its sale. Draft and Withdrawn entries
// are not counted.
type TicketTypeStats struct {
	// EntryCount is the number of Entered, Won and Lost entries.
	EntryCount int

	// RequestedTicketCount is the sum of those entries' requested ticket
	// counts.
	RequestedTicketCount int

	// WonEntryCount is the number of Won entries.
	WonEntryCount int

	// WonTicketCount is the sum of the Won entries' requested ticket counts.
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

	// PaymentIntentRef is the entry's card hold, which the draw captures.
	PaymentIntentRef string

	// DrawPosition is the entry's zero-based position in the draw order.
	DrawPosition int
}

// DrawLoserRow carries what persisting one losing draw outcome needs. Built
// from [RunLotteryDraw]'s result and consumed by
// [LotteryEntryRepository.PersistDrawOutcome].
type DrawLoserRow struct {
	// LotteryEntryID is the losing entry.
	LotteryEntryID LotteryEntryID

	// PaymentIntentRef is the entry's card hold, which the draw releases.
	PaymentIntentRef string

	// DrawPosition is the entry's zero-based position in the draw order; it
	// keeps the loser waitlist order for official resale.
	DrawPosition int
}

// PaymentAuthorizationPort is the card hold of a lottery entry: a
// manual-capture authorization in yen that rejects American Express, whose
// hold cannot last the up-to-14-day window. Implementations live in
// internal/infrastructure/payment/.
//
// Hold flow:
//  1. [PaymentAuthorizationPort.CreateAuthorization] at Authorize — opens the
//     hold and returns the client secret the browser completes 3DS with.
//  2. [PaymentAuthorizationPort.VerifyAuthorization] at Confirm — checks the
//     hold is authenticated, for the expected amount, on an accepted brand.
//  3. [PaymentAuthorizationPort.CancelAuthorization] — releases the hold (a
//     loss, a withdrawal or a Draft left unconfirmed at the draw).
//  4. [PaymentAuthorizationPort.CaptureAuthorization] — charges the hold (a
//     win, by the draw).
type PaymentAuthorizationPort interface {
	// CreateAuthorization opens a hold in yen for amountJPY that captures
	// nothing until CaptureAuthorization runs, and returns the hold's
	// reference and the confirmation secret the browser authenticates with.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amountJPY is zero or negative.
	//  - Unavailable: card payments cannot be reached.
	//  - Internal: unexpected provider error.
	CreateAuthorization(ctx context.Context, amountJPY int64) (paymentIntentRef string, clientSecret string, err error)

	// VerifyAuthorization succeeds only when the hold completed card
	// authentication and waits to be captured, for exactly expectedAmountJPY
	// in yen, on an accepted card brand ([IsAcceptedCardBrand]).
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the hold is not authenticated or not waiting to be
	//    captured, or the card is American Express.
	//  - InvalidArgument: the amount differs or the currency is not yen.
	//  - NotFound: no hold has the reference.
	//  - Unavailable: card payments cannot be reached.
	VerifyAuthorization(ctx context.Context, paymentIntentRef string, expectedAmountJPY int64) error

	// CancelAuthorization releases the hold; repeating it succeeds without a
	// second effect.
	//
	// # Possible errors
	//
	//  - NotFound: no hold has the reference.
	//  - FailedPrecondition: the hold has already been captured.
	//  - Unavailable: card payments cannot be reached.
	CancelAuthorization(ctx context.Context, paymentIntentRef string) error

	// CaptureAuthorization charges the held amount; repeating it succeeds
	// without charging again.
	//
	// # Possible errors
	//
	//  - NotFound: no hold has the reference.
	//  - FailedPrecondition: the hold can no longer be captured (released,
	//    expired, or the card was closed).
	//  - Unavailable: card payments cannot be reached.
	CaptureAuthorization(ctx context.Context, paymentIntentRef string) error
}

// LotteryEntryRepository persists [LotteryEntry] records. Implementations live
// in internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type LotteryEntryRepository interface {
	// GetOrCreateDraft returns the user's Draft for the TicketType when its
	// requested ticket count equals count, changing nothing. When the user has
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

	// SetAuthorization records paymentIntentRef as the Draft's card hold. It
	// changes no other attribute.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the entry is not a Draft.
	//  - NotFound: no entry has the id.
	//  - Internal: database failure.
	SetAuthorization(ctx context.Context, id LotteryEntryID, paymentIntentRef string) error

	// Get returns the entry with the given id, in any state.
	//
	// # Possible errors
	//
	//  - NotFound: no entry has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id LotteryEntryID) (*LotteryEntry, error)

	// GetByPaymentIntentRef returns the entry whose card hold has the given
	// reference.
	//
	// # Possible errors
	//
	//  - NotFound: no entry has the reference.
	//  - Internal: database failure.
	GetByPaymentIntentRef(ctx context.Context, paymentIntentRef string) (*LotteryEntry, error)

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

	// GetTicketTypeStats tallies the TicketType's Entered, Won and Lost
	// entries. A TicketType without entries yields all counts zero.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	GetTicketTypeStats(ctx context.Context, ticketTypeID TicketTypeID) (TicketTypeStats, error)

	// PersistDrawOutcome records one sale's draw in one transaction: every
	// winner Won and every loser Lost with its draw position, every
	// unconfirmed Draft in withdrawnDrafts Withdrawn, across all TicketTypes of
	// the sale, and the sale's drawn time. With no winners, losers or Drafts it
	// only sets the drawn time. On failure nothing is recorded.
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
