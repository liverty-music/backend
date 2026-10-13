package entity

import (
	"context"
	"time"
)

// ReminderStage identifies which point of a TicketSale's timeline a reminder
// marks.
//
// Which stages apply depends on the sale's method: a Lottery gets all three, a
// FirstCome sale only APPLY_OPEN. Value 3 was APPLY_CLOSE_1H and is left
// unused. The ticket_sale_reminders.stage CHECK (1..10) leaves room for future
// stages.
type ReminderStage int16

const (
	// ReminderStageApplyOpen is anchored at the start time of a Lottery and 30
	// minutes before it for a FirstCome sale.
	ReminderStageApplyOpen ReminderStage = 1
	// ReminderStageApplyClose24H is anchored 24 hours before a Lottery's end
	// time.
	ReminderStageApplyClose24H ReminderStage = 2
	// ReminderStageResultDay is anchored on a Lottery's result time (see
	// [TicketSale.ResultTime]); it is due at 09:00 in the fan's time zone on
	// that calendar day.
	ReminderStageResultDay ReminderStage = 4
)

// FirstComeOpenLead is how long before a FirstCome sale opens its
// APPLY_OPEN stage is anchored.
const FirstComeOpenLead = 30 * time.Minute

// IsValid reports whether s is one of the defined stages.
func (s ReminderStage) IsValid() bool {
	switch s {
	case ReminderStageApplyOpen, ReminderStageApplyClose24H, ReminderStageResultDay:
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
	case ReminderStageResultDay:
		return "RESULT_DAY"
	default:
		return "UNSPECIFIED"
	}
}

// Anchor returns the milestone of sale that stage s is anchored on, alike for
// a discovered sale and an Organizer's sale:
//
//   - APPLY_OPEN: a Lottery's start time; 30 minutes before a FirstCome
//     sale's start time.
//   - APPLY_CLOSE_24H: 24 hours before a Lottery's end time.
//   - RESULT_DAY: a Lottery's result time; the stage is due on its calendar
//     day.
//
// ok is false when the stage does not apply to the sale: an undefined stage,
// a stage of the other method, or RESULT_DAY on a Lottery without a result
// time.
func (s ReminderStage) Anchor(sale *TicketSale) (anchor time.Time, ok bool) {
	lottery := sale.Method == TicketSaleMethodLottery
	switch {
	case s == ReminderStageApplyOpen && sale.Method == TicketSaleMethodFirstCome:
		return sale.SaleStartTime.Add(-FirstComeOpenLead), true
	case s == ReminderStageApplyOpen && lottery:
		return sale.SaleStartTime, true
	case s == ReminderStageApplyClose24H && lottery && !sale.SaleEndTime.IsZero():
		return sale.SaleEndTime.Add(-24 * time.Hour), true
	case s == ReminderStageResultDay && lottery:
		return sale.ResultTime()
	default:
		return time.Time{}, false
	}
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
