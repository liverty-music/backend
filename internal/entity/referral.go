package entity

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// ReferralCode is the code a link to an event page carries in `?ref=`: 1 to
// 32 characters, each a lowercase ASCII letter, a digit or a hyphen. It is
// free-form: not registered in advance, it grants no access and pays no
// reward. The fan app lowercases a link's code before it keeps it, so
// `Member-A` and `member-a` in links are one code.
//
// Mirrors liverty_music.entity.v1.ReferralCode.value.
type ReferralCode string

// referralCodePattern is the ReferralCode format; the proto rule and the
// chk_referrals_code constraint repeat it.
var referralCodePattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Validate reports whether c is a valid ReferralCode.
func (c ReferralCode) Validate() error {
	if !referralCodePattern.MatchString(string(c)) {
		return errors.New("referral code must be 1 to 32 lowercase ASCII letters, digits or hyphens")
	}
	return nil
}

// ReferralID is the opaque identifier for a single Referral (a UUIDv7
// string).
type ReferralID string

// ReferralSource is the purchase in progress a Referral belongs to: exactly
// one of a Checkout and a LotteryEntry.
type ReferralSource struct {
	// CheckoutID names the Checkout; empty when the source is a LotteryEntry.
	CheckoutID CheckoutID
	// LotteryEntryID names the LotteryEntry; empty when the source is a
	// Checkout.
	LotteryEntryID LotteryEntryID
}

// Validate reports whether s names exactly one source.
func (s ReferralSource) Validate() error {
	if (s.CheckoutID == "") == (s.LotteryEntryID == "") {
		return errors.New("referral source must be exactly one of a checkout and a lottery entry")
	}
	return nil
}

// Referral records that one Checkout or one LotteryEntry was started from a
// link that carried a referral code. At most one Referral exists per source,
// and it is removed with its source. Its code changes only while the source
// is open (see [CheckoutStatus.AcceptsReferral] and
// [LotteryEntryState.AcceptsReferral]).
type Referral struct {
	// ID is the referral's identity, assigned on creation.
	ID ReferralID
	// Source is the Checkout or LotteryEntry started with the code.
	Source ReferralSource
	// Code is the ReferralCode the source was started with.
	Code ReferralCode
	// CreateTime is when the source was first started with a code; set once.
	CreateTime time.Time
}

// Validate reports whether r names exactly one source and holds a valid code.
func (r *Referral) Validate() error {
	if err := r.Source.Validate(); err != nil {
		return err
	}
	return r.Code.Validate()
}

// AcceptsReferral reports whether a Referral of a Checkout with this status
// may still change: only while the Checkout is Held.
func (s CheckoutStatus) AcceptsReferral() bool {
	return s == CheckoutStatusHeld
}

// AcceptsReferral reports whether a Referral of a LotteryEntry in this state
// may still change: only while the entry is a Draft.
func (s LotteryEntryState) AcceptsReferral() bool {
	return s == LotteryEntryStateDraft
}

// ReferralCodeStats counts the Paid Orders of one event whose source was
// started with one referral code.
type ReferralCodeStats struct {
	// Code is the referral code; nil for the direct group, the Orders whose
	// source has no Referral.
	Code *ReferralCode
	// OrderCount is the number of Paid Orders.
	OrderCount int
	// TicketCount is the sum of the ticket counts of the Orders' sources.
	TicketCount int
	// Amount is the sum of the Orders' amounts, in whole yen.
	Amount int64
}

// ReferralRepository persists Referrals and counts purchases per referral
// code. Implementations live in internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type ReferralRepository interface {
	// Record stores the code the source was started with at the time at. A
	// source without a Referral gets one with at as its create time; a
	// Referral with another code has the code replaced and keeps its create
	// time; the same code changes nothing. Only an open source (a Held
	// Checkout or a Draft LotteryEntry) is changed: for any other, Record
	// changes nothing and succeeds. Checking the source and writing are one
	// indivisible step.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the source or the code is invalid.
	//  - FailedPrecondition: the source does not exist; nothing is stored.
	//  - Internal: database failure.
	Record(ctx context.Context, source ReferralSource, code ReferralCode, at time.Time) error

	// ListStatsByEvent counts the event's Paid Orders per referral code of
	// their source, with the Orders whose source has no Referral as one
	// direct group (nil Code). Only groups with at least one Order are
	// returned, the most Orders first, then by code compared character by
	// character. An event without Paid Orders, or unknown, yields an empty
	// list.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListStatsByEvent(ctx context.Context, eventID string) ([]ReferralCodeStats, error)
}
