package entity

import (
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
	// (中止) while the entry was Entered, so the next draw ended it.
	ResaleEntryLostReasonConcertCancelled ResaleEntryLostReason = 2

	// ResaleEntryLostReasonCardNotCharged means the entry won but its Order's
	// card authorization could not be captured.
	ResaleEntryLostReasonCardNotCharged ResaleEntryLostReason = 3

	// ResaleEntryLostReasonPaymentNotCompletedBeforeShow means the entry won
	// but its payment could not be completed before its event's start time,
	// so its card authorization was released without a charge.
	ResaleEntryLostReasonPaymentNotCompletedBeforeShow ResaleEntryLostReason = 4
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
// and its Order Void, so the listings allocated to it are free for the next
// draw ([ResaleListing.IsFreeForDraw]). An entry already Lost is left as it is.
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
