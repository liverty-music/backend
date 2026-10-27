package entity

import (
	"errors"
	"regexp"
)

// ReferralCode is the code a link to an event page carries in `?ref=`: 1 to
// 32 characters, each a lowercase ASCII letter, a digit or a hyphen. It is
// free-form: not registered in advance, it grants no access and pays no
// reward. A Checkout or a LotteryEntry keeps it as its referral code. The
// fan app lowercases a link's code before it keeps it, so `Member-A` and
// `member-a` in links are one code.
//
// Mirrors liverty_music.entity.v1.ReferralCode.value.
type ReferralCode string

// referralCodePattern is the ReferralCode format; the proto rule and the
// chk_checkouts_referral_code and chk_lottery_entries_referral_code
// constraints repeat it.
var referralCodePattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Validate reports whether c is a valid ReferralCode.
func (c ReferralCode) Validate() error {
	if !referralCodePattern.MatchString(string(c)) {
		return errors.New("referral code must be 1 to 32 lowercase ASCII letters, digits or hyphens")
	}
	return nil
}

// ReferralCodeStats counts the Paid Orders of one event whose source, a
// Checkout or a LotteryEntry, has one referral code.
type ReferralCodeStats struct {
	// Code is the referral code; nil for the direct group, the Orders whose
	// source has no referral code.
	Code *ReferralCode
	// OrderCount is the number of Paid Orders.
	OrderCount int
	// TicketCount is the sum of the ticket counts of the Orders' sources.
	TicketCount int
	// Amount is the sum of the Orders' amounts, in whole yen.
	Amount int64
}
