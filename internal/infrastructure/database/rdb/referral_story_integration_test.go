package rdb_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The story tests of stories/attribute-a-purchase-to-a-referral-link buy
// first come with the code the fan app sends to CheckoutUseCase.Start and read
// the Organizer's Sales tab with TicketSaleUseCase.ListOwnByEvent, over the
// first-come story's wiring (real Postgres, real Stripe test API). Which code
// the fan app sends is decided on the device: the Event page keeps the code
// of the last link opened with its Series for 7 days and the checkout sends it
// only for an event of that Series (frontend
// src/adapter/storage/referral-storage.ts). The tests of "Last link wins",
// "Another tour is not attributed" and "Link older than 7 days" therefore
// start from the code the fan app sends in that case and check that the
// backend counts exactly it. The lottery path of the story's requirement is
// TestLotteryStory_EntryFromAReferralLink.

// salesTab returns the event's purchases per referral code, as the
// Organizer's Sales tab shows them.
func (s *firstComeStory) salesTab() []entity.ReferralCodeStats {
	s.t.Helper()
	own, err := s.sales.ListOwnByEvent(s.ctx, s.organizerID, s.eventID)
	require.NoError(s.t, err)
	return own.ReferralCodeStats
}

// referralCode returns a pointer to the code.
func referralCode(code string) *entity.ReferralCode {
	c := entity.ReferralCode(code)
	return &c
}

func TestReferralStory_GuestFromAMembersLinkBuysAfterSigningUp(t *testing.T) {
	// @spec stories/attribute-a-purchase-to-a-referral-link "Guest from a member's link buys after signing up"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	// The guest signs up from the Event page and buys with the kept code.
	fan := s.newFan()
	code := referralCode("member-a")
	checkout := s.hold(stock.ID, fan, 2, code)
	assert.Empty(t, s.salesTab(), "a Pending Order is not counted")

	s.place(checkout, fan, s.authorizeCheckout(checkout, fan))
	assert.Equal(t, []entity.ReferralCodeStats{{Code: code, OrderCount: 1, TicketCount: 2, Amount: 6000}}, s.salesTab(),
		"member-a with 1件 2枚 6,000円")
}

func TestReferralStory_LastLinkWins(t *testing.T) {
	// @spec stories/attribute-a-purchase-to-a-referral-link "Last link wins"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	// The fan opened member-a's link, then member-b's for the same Series:
	// the fan app keeps and sends member-b.
	fan := s.newFan()
	s.buy(stock.ID, fan, 2, referralCode("member-b"))
	assert.Equal(t, []entity.ReferralCodeStats{{Code: referralCode("member-b"), OrderCount: 1, TicketCount: 2, Amount: 6000}}, s.salesTab(),
		"counted under member-b only")
}

func TestReferralStory_HoldAlreadyInProgressKeepsItsLink(t *testing.T) {
	// @spec stories/attribute-a-purchase-to-a-referral-link "Hold already in progress keeps its link"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	// The fan held 2 tickets from member-a's link, then opened member-b's
	// link and continued the same 2 tickets: Start resumes the hold, whose
	// Order keeps member-a.
	fan := s.newFan()
	held := s.hold(stock.ID, fan, 2, referralCode("member-a"))
	resumed := s.hold(stock.ID, fan, 2, referralCode("member-b"))
	require.Equal(t, held.ID, resumed.ID, "the hold is resumed")
	s.place(resumed, fan, s.authorizeCheckout(resumed, fan))
	assert.Equal(t, []entity.ReferralCodeStats{{Code: referralCode("member-a"), OrderCount: 1, TicketCount: 2, Amount: 6000}}, s.salesTab(),
		"counted under member-a only")
}

func TestReferralStory_AnotherTourIsNotAttributed(t *testing.T) {
	// @spec stories/attribute-a-purchase-to-a-referral-link "Another tour is not attributed"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	// The kept code is for another Series, so the fan app sends none.
	fan := s.newFan()
	s.buy(stock.ID, fan, 2, nil)
	assert.Equal(t, []entity.ReferralCodeStats{{Code: nil, OrderCount: 1, TicketCount: 2, Amount: 6000}}, s.salesTab(),
		"counted as 直接（コードなし）")
}

func TestReferralStory_LinkOlderThan7Days(t *testing.T) {
	// @spec stories/attribute-a-purchase-to-a-referral-link "Link older than 7 days"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	// 8 days after the link was opened the kept code has lapsed, so the fan
	// app sends none.
	fan := s.newFan()
	s.buy(stock.ID, fan, 2, nil)
	assert.Equal(t, []entity.ReferralCodeStats{{Code: nil, OrderCount: 1, TicketCount: 2, Amount: 6000}}, s.salesTab(),
		"counted as 直接（コードなし）")
}

func TestReferralStory_AbandonedCheckoutIsNotCounted(t *testing.T) {
	// @spec stories/attribute-a-purchase-to-a-referral-link "Abandoned checkout is not counted"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	fan := s.newFan()
	checkout := s.hold(stock.ID, fan, 2, referralCode("member-a"))
	s.authorizeCheckout(checkout, fan)

	// The hold lapses and the release job ends the checkout.
	s.now = checkout.HoldExpireTime.Add(time.Minute)
	require.NoError(t, s.checkouts.ReleaseExpired(s.ctx))
	s.now = s.now.Add(time.Minute)
	require.NoError(t, s.checkouts.ReleaseExpired(s.ctx))
	assert.Equal(t, entity.OrderStatusVoid, orderStatusOf(t, checkout.OrderID))
	assert.Empty(t, s.salesTab(), "nothing is counted under member-a")
}
