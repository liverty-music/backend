package entity

import (
	"context"
	"time"
)

// ReminderStage identifies which point of a TicketSale's timeline a reminder
// marks.
//
// Which stages apply depends on the sale: a Lottery gets APPLY_OPEN and
// APPLY_CLOSE_24H, a FirstCome sale only APPLY_OPEN, and a discovered Lottery
// with a result date also RESULT_DAY. Value 3 was APPLY_CLOSE_1H and is not
// used.
type ReminderStage int16

const (
	// ReminderStageApplyOpen is anchored at the start time of a Lottery and 30
	// minutes before it for a FirstCome sale.
	ReminderStageApplyOpen ReminderStage = 1
	// ReminderStageApplyClose24H is anchored 24 hours before a Lottery's end
	// time.
	ReminderStageApplyClose24H ReminderStage = 2
	// ReminderStageResultDay is anchored at [ResultDayHour] on a discovered
	// Lottery's result date in the fan's time zone.
	ReminderStageResultDay ReminderStage = 4
)

const (
	// FirstComeOpenLead is how long before a FirstCome sale opens its
	// APPLY_OPEN stage is anchored.
	FirstComeOpenLead = 30 * time.Minute
	// ResultDayHour is the local hour on the result date at which the
	// RESULT_DAY stage is anchored.
	ResultDayHour = 9
	// ResultDayExpiryHour is the local hour on the result date at which the
	// RESULT_DAY stage expires, the start of the fan's quiet hours.
	ResultDayExpiryHour = 22
)

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

// Anchor returns the milestone of sale that stage s is anchored on for a fan
// in time zone tz, alike for a discovered sale and an Organizer's sale unless
// stated:
//
//   - APPLY_OPEN: a Lottery's start time; 30 minutes before a FirstCome
//     sale's start time.
//   - APPLY_CLOSE_24H: 24 hours before a Lottery's end time.
//   - RESULT_DAY: [ResultDayHour] on the result date in tz, on a discovered
//     Lottery with a result date only.
//
// ok is false when the stage does not apply to the sale: an undefined stage,
// a stage of the other method, or RESULT_DAY on a sale without a result date.
func (s ReminderStage) Anchor(sale *TicketSale, tz *time.Location) (anchor time.Time, ok bool) {
	lottery := sale.Method == TicketSaleMethodLottery
	switch {
	case s == ReminderStageResultDay && lottery && sale.IsDiscovered() && sale.ResultDate != nil:
		return sale.ResultDayAt(ResultDayHour, tz), true
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
