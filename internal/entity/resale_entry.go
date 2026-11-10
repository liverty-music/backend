package entity

import (
	"context"
	"errors"
	"time"
)

// ResaleEntryID is the opaque identifier for a single ResaleEntry.
//
// Mirrors liverty_music.entity.v1.ResaleEntryId.value (a UUID string).
type ResaleEntryID string

// ResaleEntryState is the lifecycle state of a [ResaleEntry]. Values mirror
// the proto enum liverty_music.entity.v1.ResaleEntryState and the stored
// state.
//
//	[*] --> Entered --> Won | Lost | Lapsed | Withdrawn
//	Won --> Lost (the win could not be charged)
type ResaleEntryState int8

const (
	// ResaleEntryStateUnspecified is the zero value and is never persisted.
	ResaleEntryStateUnspecified ResaleEntryState = 0

	// ResaleEntryStateEntered means the entry waits for a draw with its card
	// authorization in place.
	ResaleEntryStateEntered ResaleEntryState = 1

	// ResaleEntryStateWon means a draw allocated listings to the entry. Its
	// card authorization is captured afterwards, so its Order is Pending until
	// it is Paid.
	ResaleEntryStateWon ResaleEntryState = 2

	// ResaleEntryStateLost means the entry ended without tickets for its lost
	// reason. Its Order is Void and its card authorization is released.
	ResaleEntryStateLost ResaleEntryState = 3

	// ResaleEntryStateLapsed means the entry ended at a draw because its card
	// authorization would expire too soon to charge a win. Its Order is Void.
	ResaleEntryStateLapsed ResaleEntryState = 4

	// ResaleEntryStateWithdrawn means the fan withdrew the entry while it was
	// Entered. Its Order is Void.
	ResaleEntryStateWithdrawn ResaleEntryState = 5
)

// String returns the lowercase state name.
func (s ResaleEntryState) String() string {
	switch s {
	case ResaleEntryStateEntered:
		return "entered"
	case ResaleEntryStateWon:
		return "won"
	case ResaleEntryStateLost:
		return "lost"
	case ResaleEntryStateLapsed:
		return "lapsed"
	case ResaleEntryStateWithdrawn:
		return "withdrawn"
	default:
		return "UNSPECIFIED"
	}
}

// ResaleEntryLostReason is why a Lost ResaleEntry lost. The numbers are the
// stored values; an entry that is not Lost has no reason.
type ResaleEntryLostReason int16

const (
	// ResaleEntryLostReasonUnspecified is the zero value: the entry is not
	// Lost. Never persisted.
	ResaleEntryLostReasonUnspecified ResaleEntryLostReason = 0

	// ResaleEntryLostReasonNoTicketLeft means no listing was left for the
	// entry at its event's last draw.
	ResaleEntryLostReasonNoTicketLeft ResaleEntryLostReason = 1

	// ResaleEntryLostReasonConcertCancelled means the concert was cancelled
	// (中止): while the entry was Entered, so the next draw ended it, or after
	// it won but before its card authorization was captured, so nothing was
	// charged.
	ResaleEntryLostReasonConcertCancelled ResaleEntryLostReason = 2

	// ResaleEntryLostReasonCardNotCharged means the entry won but its Order's
	// card authorization could not be captured.
	ResaleEntryLostReasonCardNotCharged ResaleEntryLostReason = 3

	// ResaleEntryLostReasonPaymentNotCompletedBeforeShow means the entry won
	// but its payment could not be completed before its event's start time,
	// so its card authorization was released without a charge.
	ResaleEntryLostReasonPaymentNotCompletedBeforeShow ResaleEntryLostReason = 4

	// ResaleEntryLostReasonListingCancelled means the entry won and its card
	// authorization was captured, but a listing allocated to it was cancelled
	// before its tickets were issued, because the Order of the fan who listed
	// it was refunded; the charge was refunded in full.
	ResaleEntryLostReasonListingCancelled ResaleEntryLostReason = 5
)

// ResaleEntry is one fan account's application, through its Order, for
// official resale (公式リセール) tickets of one sold-out TicketStock: a
// companion group of tickets allocated all-or-nothing by a daily draw, paid by
// the Order's card authorization placed when the fan applies and captured only
// on a win. It belongs to no TicketSale; its supply is the stock's
// ResaleListings. The fan, the TicketStock, the ticket count, the card
// authorization and the referral code are the Order's.
//
// Mirrors liverty_music.entity.v1.ResaleEntry.
type ResaleEntry struct {
	// ID is the surrogate primary key (UUIDv7).
	ID ResaleEntryID
	// OrderID is the Order the entry applies for; one entry per Order.
	OrderID OrderID
	// State is the entry's lifecycle state.
	State ResaleEntryState
	// LostReason is why the entry lost; unspecified unless State is Lost.
	LostReason ResaleEntryLostReason
	// CreateTime is when the fan applied.
	CreateTime time.Time
	// DrawTime is the draw that ended the entry (won, lost or lapsed at); nil
	// while Entered and on a Withdrawn entry.
	DrawTime *time.Time
	// WithdrawTime is when the fan withdrew the entry; nil unless Withdrawn.
	WithdrawTime *time.Time
	// EndedEmailSendTime is when the email telling the fan that the entry
	// lapsed or lost was sent; nil until then.
	EndedEmailSendTime *time.Time
	// CardAuthorizationExpireTime is when the Order's card authorization can no
	// longer be charged; read with the entries a draw takes, nil otherwise.
	CardAuthorizationExpireTime *time.Time
	// Order is the Order the entry applies for, read with every entry.
	Order *Order
}

// NewResaleEntry returns an Entered entry for order, applied for at now, with
// a generated UUIDv7 id and no draw time, and names it as order's source.
// order is Pending and carries the card authorization the entry is made from.
func NewResaleEntry(order *Order, now time.Time) *ResaleEntry {
	e := &ResaleEntry{
		ID:         ResaleEntryID(NewID()),
		OrderID:    order.ID,
		State:      ResaleEntryStateEntered,
		CreateTime: now,
		Order:      order,
	}
	order.ResaleEntryID = e.ID
	return e
}

// IsWithdrawable reports whether the fan may withdraw the entry: only while
// it is Entered.
func (e *ResaleEntry) IsWithdrawable() bool {
	return e.State == ResaleEntryStateEntered
}

// ErrResaleWinNotRevertible is returned by [ResaleEntry.RevertWin] for an
// entry that is not a Won entry with a Pending Order.
var ErrResaleWinNotRevertible = errors.New("only a won resale entry whose order is pending can be made lost")

// RevertWin makes a Won entry whose Order is Pending Lost with the reason,
// and its Order Void, so the listings allocated to it that are still Listed
// are free for the next draw ([ResaleListing.IsFreeForDrawAt]); a Cancelled
// one never is. An entry already Lost is left as it is.
func (e *ResaleEntry) RevertWin(reason ResaleEntryLostReason) error {
	if e.State == ResaleEntryStateLost {
		return nil
	}
	if e.State != ResaleEntryStateWon || e.Order == nil || e.Order.Status != OrderStatusPending {
		return ErrResaleWinNotRevertible
	}
	e.State, e.LostReason = ResaleEntryStateLost, reason
	e.Order.Status = OrderStatusVoid
	return nil
}

// ResaleDrawHour is the hour, Japan time, of the daily resale draw.
const ResaleDrawHour = 12

// ResaleLapseLead is how long after a draw a card authorization must still be
// chargeable for its entry to take part: a Won entry is charged, with
// retries, within this time.
const ResaleLapseLead = 24 * time.Hour

// ResaleDrawAt reports whether a resale draw of the event starting at start
// runs at t, and whether it is the event's last: 12:00 Japan time on every day
// before the event's day, and the event's resale deadline on its day, which
// is the last draw ([ResaleDeadlineAt]).
func ResaleDrawAt(t, start time.Time) (draw, last bool) {
	deadline := ResaleDeadlineAt(start)
	if t.Equal(deadline) {
		return true, true
	}
	if !t.Before(deadline) {
		return false, false
	}
	local := t.In(japanTime)
	return local.Hour() == ResaleDrawHour && local.Minute() == 0 && local.Second() == 0 && local.Nanosecond() == 0, false
}

// ResaleLastDrawMissed reports whether t is after the resale deadline of the
// event starting at start. A stock that still has Entered entries then
// missed its last draw (the worker was down at the deadline, and the
// schedule does not backfill): the next run ends them as the last draw
// would have, except that it allocates no listing, since every listing
// expired at the deadline.
func ResaleLastDrawMissed(t, start time.Time) bool {
	return t.After(ResaleDeadlineAt(start))
}

// NextResaleDrawTime returns the first resale draw of the event starting at
// start that runs after now, and false when its last draw has passed.
func NextResaleDrawTime(now, start time.Time) (time.Time, bool) {
	deadline := ResaleDeadlineAt(start)
	if !now.Before(deadline) {
		return time.Time{}, false
	}
	local := now.In(japanTime)
	next := time.Date(local.Year(), local.Month(), local.Day(), ResaleDrawHour, 0, 0, 0, japanTime)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	if next.Before(deadline) {
		return next, true
	}
	return deadline, true
}

// TakesPartInDrawAt reports whether the entry takes part in a draw at
// drawTime: its card authorization is still chargeable [ResaleLapseLead] after
// the draw. An entry that does not take part lapses at that draw.
func (e *ResaleEntry) TakesPartInDrawAt(drawTime time.Time) bool {
	return e.CardAuthorizationExpireTime != nil && !e.CardAuthorizationExpireTime.Before(drawTime.Add(ResaleLapseLead))
}

// ResaleWin is one entry a draw makes Won with the listings allocated to it.
type ResaleWin struct {
	// Entry is the winning entry.
	Entry *ResaleEntry
	// Listings are the listings allocated to it, as many as its ticket count.
	Listings []ResaleListingID
}

// ResaleDrawOutcome is what one draw of one TicketStock records: the winners
// with their listings, the entries that lapse and the entries that lose, each
// with its lost reason. An entry in none of them stays Entered.
type ResaleDrawOutcome struct {
	// Winners become Won with their listings.
	Winners []ResaleWin
	// Lapsed become Lapsed.
	Lapsed []*ResaleEntry
	// Lost become Lost with LostReason.
	Lost []*ResaleEntry
	// LostReason is the reason of every entry in Lost.
	LostReason ResaleEntryLostReason
}

// DrawResale allocates the free listings of a TicketStock, oldest first, to
// its Entered entries in the given (random) order, all-or-nothing: an entry
// wins exactly as many listings as its Order's ticket count when that many are
// still free at its turn, and gets none otherwise. An entry whose card
// authorization would lapse before a win could be charged lapses. At the last
// draw every entry that did not win or lapse is Lost with no ticket left; at
// any other draw it stays Entered. When the concert is cancelled the draw
// allocates nothing and every entry is Lost with the concert cancelled.
func DrawResale(entries []*ResaleEntry, freeListings []ResaleListingID, drawTime time.Time, last, cancelled bool) ResaleDrawOutcome {
	if cancelled {
		return ResaleDrawOutcome{Lost: entries, LostReason: ResaleEntryLostReasonConcertCancelled}
	}
	var out ResaleDrawOutcome
	if last {
		out.LostReason = ResaleEntryLostReasonNoTicketLeft
	}
	remaining := freeListings
	for _, e := range entries {
		switch {
		case !e.TakesPartInDrawAt(drawTime):
			out.Lapsed = append(out.Lapsed, e)
		case e.Order.TicketCount <= len(remaining):
			out.Winners = append(out.Winners, ResaleWin{Entry: e, Listings: remaining[:e.Order.TicketCount:e.Order.TicketCount]})
			remaining = remaining[e.Order.TicketCount:]
		case last:
			out.Lost = append(out.Lost, e)
		}
	}
	return out
}

// ResaleDrawStock is a TicketStock with Entered resale entries, with its event
// and the event's start time, from which its draw times follow.
type ResaleDrawStock struct {
	// StockID is the TicketStock.
	StockID TicketStockID
	// EventID is its event.
	EventID string
	// StartTime is the event's start time; nil when it has none.
	StartTime *time.Time
}

// ResaleEntryEndedEmail is the finished message telling a fan that their
// entry lapsed or lost, already rendered in the language of its Order's user.
type ResaleEntryEndedEmail struct {
	// Subject is the email subject line.
	Subject string
	// TextBody is the plain-text email body.
	TextBody string
}

// ResaleEntryEndedEmailSender sends a Lapsed or Lost entry's ended email at
// most once. The implementation lives in internal/infrastructure/mail/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type ResaleEntryEndedEmailSender interface {
	// SendEndedEmail sends msg to the address when the entry has no ended
	// email send time, then sets that time. When the time is already set it
	// sends nothing and succeeds. A failure between sending and recording can
	// lead to a second email on retry; no other case sends twice.
	//
	// # Possible errors
	//
	//   - InvalidArgument: the address or the message is empty.
	//   - FailedPrecondition: the entry is not Lapsed or Lost.
	//   - Unavailable: email cannot be sent; the send time stays unset.
	//   - NotFound: no entry has the id.
	SendEndedEmail(ctx context.Context, id ResaleEntryID, to string, msg ResaleEntryEndedEmail) error
}

// ResaleEntryRepository persists [ResaleEntry] records together with their
// Orders. Every read returns the entry with its Order. Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type ResaleEntryRepository interface {
	// Create stores the new Entered entry together with its Pending Order
	// (entry.Order, which carries the authorized card authorization) at the
	// entry's create time, and records the card authorization's expiry time
	// (entry.CardAuthorizationExpireTime) for the draws. When an Order
	// already holds the card authorization, it stores nothing and returns
	// that Order's entry. It locks the user's users row first, so checking
	// and storing are one indivisible step with every other creation of an
	// Order by the user.
	//
	// # Possible errors
	//
	//  - AlreadyExists: the user already has an Entered resale entry for the
	//    stock's event; nothing is stored.
	//  - FailedPrecondition: the stock's resale is not open at the create
	//    time, or the application does not fit the stock's event
	//    ([Event.Fits] with [OrderRepository.GetHeldForEvent]); nothing is
	//    stored.
	//  - NotFound: the TicketStock or the user does not exist; nothing is
	//    stored.
	//  - Unavailable: a lock wait timed out; nothing is stored.
	//  - Internal: database failure.
	Create(ctx context.Context, entry *ResaleEntry) (*ResaleEntry, error)

	// Get returns the entry with the given id, in any state.
	//
	// # Possible errors
	//
	//  - NotFound: no entry has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id ResaleEntryID) (*ResaleEntry, error)

	// GetByCardAuthorizationID returns the entry whose Order's card
	// authorization has the given id.
	//
	// # Possible errors
	//
	//  - NotFound: there is none.
	//  - Internal: database failure.
	GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*ResaleEntry, error)

	// ListByUser returns every entry whose Order's user is the user, in every
	// state, newest first. It returns an empty slice when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByUser(ctx context.Context, userID UserID) ([]*ResaleEntry, error)

	// ListEnteredByTicketStock returns every Entered entry whose Order is of
	// the stock, each with its card authorization's expiry time, in no
	// particular order. It returns an empty slice when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListEnteredByTicketStock(ctx context.Context, stockID TicketStockID) ([]*ResaleEntry, error)

	// PersistDrawOutcome records one draw of the stock at drawTime in one
	// transaction under the stock's row lock, which ResaleListing.Withdraw
	// and ResaleEntry.Withdraw also take: every winner becomes Won with the
	// draw time and its listings are allocated to it, every lapsing entry
	// becomes Lapsed and every loser Lost with the outcome's lost reason, both
	// with the draw time and their Orders Void. Every entry must still be
	// Entered and every listing still Listed and allocated to no entry or to
	// a Lost one; otherwise nothing is recorded.
	//
	// # Possible errors
	//
	//   - FailedPrecondition: an entry is no longer Entered or a listing is no
	//     longer free for a draw; nothing is recorded.
	//   - Unavailable: an announcement cannot be recorded; nothing is recorded.
	//   - Internal: database failure.
	//
	// # Announces
	//
	// RESALE_ENTRY.won, RESALE_ENTRY.lapsed or RESALE_ENTRY.lost once per entry
	// it makes Won, Lapsed or Lost, with the entry as it is after the draw,
	// together with the outcome.
	PersistDrawOutcome(ctx context.Context, stockID TicketStockID, drawTime time.Time, outcome ResaleDrawOutcome) error

	// Withdraw makes the Entered entry Withdrawn at the time and its Order
	// Void, in one transaction under the row lock of its Order's TicketStock,
	// the lock a draw takes before it reads the Entered entries (design D8),
	// and returns the entry as it is after the change.
	//
	// # Possible errors
	//
	//   - NotFound: no entry has the id.
	//   - FailedPrecondition: the entry is no longer Entered, also when a draw
	//     running at the same moment recorded it first; nothing changes.
	//   - Unavailable: the announcement cannot be recorded; nothing changes.
	//   - Internal: database failure.
	//
	// # Announces
	//
	// RESALE_ENTRY.withdrawn with the Withdrawn entry, together with the
	// change. Nothing when it fails.
	Withdraw(ctx context.Context, id ResaleEntryID, at time.Time) (*ResaleEntry, error)

	// DrawUnderLock runs one draw of the stock at drawTime in one transaction
	// under the stock's row lock: it reads the stock's Entered entries (as
	// ListEnteredByTicketStock) and its listings free for a draw, oldest
	// first, passes them to decide and records decide's outcome as
	// PersistDrawOutcome does, so nothing it reads changes before it records
	// (design D4). It returns the outcome it recorded.
	//
	// # Possible errors
	//
	//   - Unavailable: an announcement cannot be recorded; nothing is recorded.
	//   - Internal: database failure.
	//
	// # Announces
	//
	// RESALE_ENTRY.won, RESALE_ENTRY.lapsed or RESALE_ENTRY.lost once per entry
	// it makes Won, Lapsed or Lost.
	DrawUnderLock(ctx context.Context, stockID TicketStockID, drawTime time.Time, decide func(entries []*ResaleEntry, freeListings []ResaleListingID) ResaleDrawOutcome) (ResaleDrawOutcome, error)

	// ListDueStocks returns every TicketStock with Entered entries, with its
	// event and the event's start time.
	//
	// # Possible errors
	//
	//   - Internal: database failure.
	ListDueStocks(ctx context.Context) ([]ResaleDrawStock, error)

	// RevertWin makes a Won entry whose Order is Pending Lost with the lost
	// reason (card not charged, payment not completed before the show,
	// concert cancelled, or listing cancelled after the charge was refunded)
	// and its Order Void, in one transaction under the row lock of its Order's
	// TicketStock, so the listings allocated to it that are still Listed are
	// free for the next draw. An entry already Lost is left as it is.
	//
	// # Possible errors
	//
	//   - FailedPrecondition: the entry is Entered, Lapsed or Withdrawn, or
	//     its Order is Paid or Refunded; nothing changes.
	//   - NotFound: no entry has the id.
	//   - Unavailable: the announcement cannot be recorded; nothing changes.
	//   - Internal: database failure.
	//
	// # Announces
	//
	// RESALE_ENTRY.lost with the entry as Lost, together with the
	// change. Nothing when the entry was already Lost.
	RevertWin(ctx context.Context, id ResaleEntryID, reason ResaleEntryLostReason) error

	// MarkEndedEmailSent sets the entry's ended email send time when it is
	// unset; an already set time is kept.
	//
	// # Possible errors
	//
	//   - NotFound: no entry has the id.
	//   - Internal: database failure.
	MarkEndedEmailSent(ctx context.Context, id ResaleEntryID, at time.Time) error
}
