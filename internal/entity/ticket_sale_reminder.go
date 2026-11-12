package entity

import (
	"context"
	"time"
)

// ReminderStage identifies which point of a TicketSale's timeline a reminder
// marks.
//
// Which stages apply depends on the sale: a Lottery gets APPLY_OPEN and
// APPLY_CLOSE_24H, a FirstCome sale only APPLY_OPEN. Values 3 (APPLY_CLOSE_1H)
// and 4 (RESULT_DAY) are not used.
type ReminderStage int16

const (
	// ReminderStageApplyOpen is anchored at the start time of a Lottery and 30
	// minutes before it for a FirstCome sale.
	ReminderStageApplyOpen ReminderStage = 1
	// ReminderStageApplyClose24H is anchored 24 hours before a Lottery's end
	// time.
	ReminderStageApplyClose24H ReminderStage = 2
)

// FirstComeOpenLead is how long before a FirstCome sale opens its APPLY_OPEN
// stage is anchored.
const FirstComeOpenLead = 30 * time.Minute

// IsValid reports whether s is one of the defined stages.
func (s ReminderStage) IsValid() bool {
	switch s {
	case ReminderStageApplyOpen, ReminderStageApplyClose24H:
		return true
	default:
		return false
	}
}

// String returns the uppercase name of the stage for use in analytics event
// properties (e.g. "APPLY_OPEN"). The zero value and any unrecognised int16
// return "UNSPECIFIED".
func (s ReminderStage) String() string {
	switch s {
	case ReminderStageApplyOpen:
		return "APPLY_OPEN"
	case ReminderStageApplyClose24H:
		return "APPLY_CLOSE_24H"
	default:
		return "UNSPECIFIED"
	}
}

// Anchor returns the milestone of sale that stage s is anchored on, alike for a
// discovered sale and an Organizer's sale:
//
//   - APPLY_OPEN: a Lottery's start time; 30 minutes before a FirstCome
//     sale's start time.
//   - APPLY_CLOSE_24H: 24 hours before a Lottery's end time.
//
// ok is false when the stage does not apply to the sale: an undefined stage or
// a stage of the other method.
func (s ReminderStage) Anchor(sale *TicketSale) (anchor time.Time, ok bool) {
	lottery := sale.Method == TicketSaleMethodLottery
	switch {
	case s == ReminderStageApplyOpen && sale.Method == TicketSaleMethodFirstCome:
		return sale.SaleStartTime.Add(-FirstComeOpenLead), true
	case s == ReminderStageApplyOpen && lottery:
		return sale.SaleStartTime, true
	case s == ReminderStageApplyClose24H && lottery:
		return sale.SaleEndTime.Add(-24 * time.Hour), true
	default:
		return time.Time{}, false
	}
}

// ReminderAudience is whose reminders a sale is about: the fans tracking the
// sale's Series, or the fans tracking the events the sale names.
type ReminderAudience struct {
	// SeriesID is the Series whose trackers are reminded; empty when EventIDs
	// is the audience.
	SeriesID string
	// EventIDs are the events whose trackers are reminded; empty when SeriesID
	// is the audience.
	EventIDs []string
}

// ReminderAudience returns whom the sale's reminders go to: for a discovered
// sale every fan tracking an event of its Series, since it covers the tour;
// for an Organizer's sale only the fans tracking an event it names, because it
// may cover only some events of the tour.
func (s *TicketSale) ReminderAudience() ReminderAudience {
	if s.IsDiscovered() {
		return ReminderAudience{SeriesID: s.SeriesID}
	}
	return ReminderAudience{EventIDs: s.EventIDs}
}

// ReminderCanBeSent reports whether a reminder request can be sent: it names
// the event the reminder links the fan to. The scan always names one, so a
// request without it is skipped, neither sent nor failed.
func ReminderCanBeSent(linkEventID string) bool {
	return linkEventID != ""
}

// ReminderOutcome is how the delivery of one reminder ended.
type ReminderOutcome int8

const (
	// ReminderOutcomeDelivered means at least one push subscription accepted
	// the reminder.
	ReminderOutcomeDelivered ReminderOutcome = iota + 1
	// ReminderOutcomeNoSubscription means the fan has no push device to
	// deliver to: there is nothing to retry.
	ReminderOutcomeNoSubscription
	// ReminderOutcomeFailed means the send failed, or its subject no longer
	// exists.
	ReminderOutcomeFailed
)

// NewReminderOutcome returns how a delivery ended, from whether a
// subscription accepted it and whether the fan had no subscription.
func NewReminderOutcome(delivered, noSubscription bool) ReminderOutcome {
	switch {
	case delivered:
		return ReminderOutcomeDelivered
	case noSubscription:
		return ReminderOutcomeNoSubscription
	}
	return ReminderOutcomeFailed
}

// IsSent reports whether the reminder counts as sent, so the next scan does
// not request it again: it was delivered, or the fan has no subscription (a
// retry would find none). A failed one is left unsent, and the next scan
// requests it again while its stage is due.
func (o ReminderOutcome) IsSent() bool {
	return o == ReminderOutcomeDelivered || o == ReminderOutcomeNoSubscription
}

// String returns the outcome as the delivery-reliability log names it:
// "delivered", "no_subscription" or "failed".
func (o ReminderOutcome) String() string {
	switch o {
	case ReminderOutcomeDelivered:
		return "delivered"
	case ReminderOutcomeNoSubscription:
		return "no_subscription"
	}
	return "failed"
}

// TicketSaleReminderRepository persists the sent-log of ticket sale reminders.
// It keeps one record per (user, ticket sale, stage).
type TicketSaleReminderRepository interface {
	// RecordSent records that the stage reminder of the sale was sent to the
	// user, with the current time as sent time. Recording the same user, sale
	// and stage again succeeds and changes nothing.
	//
	// # Possible errors
	//
	//  - InvalidArgument: userID or saleID is empty.
	//  - Internal: unexpected database failure.
	RecordSent(ctx context.Context, userID string, saleID TicketSaleID, stage ReminderStage) error

	// AlreadySent reports whether the stage reminder of the sale is recorded
	// as sent to the user.
	//
	// # Possible errors
	//
	//  - InvalidArgument: userID or saleID is empty.
	//  - Internal: unexpected database failure.
	AlreadySent(ctx context.Context, userID string, saleID TicketSaleID, stage ReminderStage) (bool, error)

	// ListSentStages returns, for each given user with at least one record for
	// the sale, the set of stages recorded. Users without a record are absent;
	// no users given returns an empty map.
	//
	// # Possible errors
	//
	//  - InvalidArgument: saleID is empty.
	//  - Internal: unexpected database failure.
	ListSentStages(ctx context.Context, saleID TicketSaleID, userIDs []string) (map[string]map[ReminderStage]bool, error)
}
