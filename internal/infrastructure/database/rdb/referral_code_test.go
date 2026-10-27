package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedCheckoutWithStatus inserts a checkout of count tickets for amount yen
// on the ticket type in the given status, with the commit and capture times
// that status requires, and the referral code unless it is empty, and returns
// its id.
func seedCheckoutWithStatus(t *testing.T, ticketTypeID entity.TicketTypeID, status entity.CheckoutStatus, count int, amount int64, code entity.ReferralCode) entity.CheckoutID {
	t.Helper()
	id := entity.NewID()
	now := time.Now()
	var authorization, commitAt, captureAt, referralCode any
	switch status {
	case entity.CheckoutStatusCommitted:
		authorization, commitAt = "pi_"+id, now
	case entity.CheckoutStatusCompleted:
		authorization, commitAt, captureAt = "pi_"+id, now, now
	}
	if code != "" {
		referralCode = string(code)
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, card_authorization_id, status, hold_expire_at, commit_at, capture_at, referral_code)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, string(ticketTypeID), seedFanUser(t), count, amount, authorization, int16(status), now.Add(15*time.Minute), commitAt, captureAt, referralCode)
	require.NoError(t, err)
	return entity.CheckoutID(id)
}

// seedLotteryEntryWithState inserts a lottery entry of count tickets on the
// ticket type in the given state for userID, with the card authorization
// "pi_<id>", and returns its id.
func seedLotteryEntryWithState(t *testing.T, ticketTypeID entity.TicketTypeID, userID string, state entity.LotteryEntryState, count int) entity.LotteryEntryID {
	t.Helper()
	return seedLotteryEntryWithCode(t, ticketTypeID, userID, state, count, "")
}

// seedLotteryEntryWithCode is seedLotteryEntryWithState with the referral
// code, unless it is empty.
func seedLotteryEntryWithCode(t *testing.T, ticketTypeID entity.TicketTypeID, userID string, state entity.LotteryEntryState, count int, code entity.ReferralCode) entity.LotteryEntryID {
	t.Helper()
	id := entity.NewID()
	var referralCode any
	if code != "" {
		referralCode = string(code)
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state, referral_code)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, string(ticketTypeID), userID, count, "pi_"+id, int16(state), referralCode)
	require.NoError(t, err)
	return entity.LotteryEntryID(id)
}

// seedOrderOf inserts an order of the given status and amount for the
// source: the checkout, or else the lottery entry.
func seedOrderOf(t *testing.T, checkoutID entity.CheckoutID, entryID entity.LotteryEntryID, status entity.OrderStatus, amount int64) {
	t.Helper()
	id := entity.NewID()
	var checkout, entry any
	if checkoutID != "" {
		checkout = string(checkoutID)
	} else {
		entry = string(entryID)
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO orders (id, user_id, checkout_id, lottery_entry_id, provider, provider_payment_id, status, amount, currency, pay_at)
		VALUES ($1, $2, $3, $4, 1, $5, $6, $7, 'JPY', now())`,
		id, seedFanUser(t), checkout, entry, "pi_"+id, int16(status), amount)
	require.NoError(t, err)
}

func TestCheckoutRepository_SetReferralCode(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	codeOf := func(t *testing.T, id entity.CheckoutID) *entity.ReferralCode {
		t.Helper()
		c, err := repo.Get(ctx, id)
		require.NoError(t, err)
		return c.ReferralCode
	}
	code := func(c entity.ReferralCode) *entity.ReferralCode { return &c }

	t.Run("first code", func(t *testing.T) {
		// @spec components/entity/checkout/set-referral-code "First code"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		id := seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusHeld, 2, 6000, "")

		require.NoError(t, repo.SetReferralCode(ctx, id, "member-a"))

		assert.Equal(t, code("member-a"), codeOf(t, id))
	})

	t.Run("last link wins", func(t *testing.T) {
		// @spec components/entity/checkout/set-referral-code "Last link wins"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		id := seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusHeld, 2, 6000, "member-a")

		require.NoError(t, repo.SetReferralCode(ctx, id, "member-b"))

		assert.Equal(t, code("member-b"), codeOf(t, id))
	})

	t.Run("committed checkout keeps its code", func(t *testing.T) {
		// @spec components/entity/checkout/set-referral-code "Committed checkout keeps its code"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		id := seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusCommitted, 2, 6000, "member-a")

		require.NoError(t, repo.SetReferralCode(ctx, id, "member-b"))

		assert.Equal(t, code("member-a"), codeOf(t, id))
	})

	t.Run("unknown checkout", func(t *testing.T) {
		// @spec components/entity/checkout/set-referral-code "Unknown checkout"
		err := repo.SetReferralCode(ctx, entity.CheckoutID(entity.NewID()), "member-a")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

func TestOrderRepository_ListReferralStatsByEvent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewOrderRepository(testDB)
	ctx := context.Background()
	code := func(c entity.ReferralCode) *entity.ReferralCode { return &c }
	// paidCheckout makes a Completed checkout of count tickets on the ticket
	// type with the code (none when empty) and an order of amount yen.
	paidCheckout := func(t *testing.T, tt *entity.TicketType, refCode entity.ReferralCode, status entity.OrderStatus, count int, amount int64) {
		t.Helper()
		id := seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusCompleted, count, amount, refCode)
		seedOrderOf(t, id, "", status, amount)
	}

	t.Run("two codes and direct purchases", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Two codes and direct purchases"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		paidCheckout(t, tt, "member-a", entity.OrderStatusPaid, 2, 6500)
		paidCheckout(t, tt, "member-a", entity.OrderStatusPaid, 3, 9750)
		paidCheckout(t, tt, "", entity.OrderStatusPaid, 1, 3250)

		got, err := repo.ListReferralStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: code("member-a"), OrderCount: 2, TicketCount: 5, Amount: 16250},
			{Code: nil, OrderCount: 1, TicketCount: 1, Amount: 3250},
		}, got)
	})

	t.Run("lottery win counted once its order exists", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Lottery win counted once its Order exists"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 0)
		entryID := seedLotteryEntryWithCode(t, tt.ID, seedFanUser(t), entity.LotteryEntryStateEntered, 2, "member-b")

		before, err := repo.ListReferralStatsByEvent(ctx, tt.EventID)
		require.NoError(t, err)
		assert.Empty(t, before, "an Entered entry without an Order is not counted")

		_, err = testDB.Pool.Exec(ctx, `UPDATE lottery_entries SET state = 2 WHERE id = $1`, string(entryID))
		require.NoError(t, err)
		seedOrderOf(t, "", entryID, entity.OrderStatusPaid, 16000)

		after, err := repo.ListReferralStatsByEvent(ctx, tt.EventID)
		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: code("member-b"), OrderCount: 1, TicketCount: 2, Amount: 16000},
		}, after)
	})

	t.Run("refunded order excluded", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Refunded order excluded"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		paidCheckout(t, tt, "member-a", entity.OrderStatusRefunded, 2, 6000)
		paidCheckout(t, tt, "", entity.OrderStatusPaid, 1, 3000)

		got, err := repo.ListReferralStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: nil, OrderCount: 1, TicketCount: 1, Amount: 3000},
		}, got)
	})

	t.Run("abandoned checkout not counted", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Abandoned checkout not counted"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusExpired, 2, 6000, "member-c")

		got, err := repo.ListReferralStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("other events not counted", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Other events not counted"
		events := seedFirstPartyEvents(t, "PUBLISHED", "PUBLISHED")
		this := seedTicketTypeForEvent(t, events[0], entity.TicketSaleMethodFirstCome, 100, 0)
		other := seedTicketTypeForEvent(t, events[1], entity.TicketSaleMethodFirstCome, 100, 0)
		paidCheckout(t, this, "member-a", entity.OrderStatusPaid, 1, 3000)
		paidCheckout(t, other, "member-a", entity.OrderStatusPaid, 2, 6000)

		got, err := repo.ListReferralStatsByEvent(ctx, events[0])

		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: code("member-a"), OrderCount: 1, TicketCount: 1, Amount: 3000},
		}, got)
	})

	t.Run("no purchases", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "No purchases"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)

		got, err := repo.ListReferralStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("groups ordered by order count then code", func(t *testing.T) {
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		paidCheckout(t, tt, "zz", entity.OrderStatusPaid, 1, 3000)
		paidCheckout(t, tt, "b", entity.OrderStatusPaid, 1, 3000)
		paidCheckout(t, tt, "a-1", entity.OrderStatusPaid, 1, 3000)
		paidCheckout(t, tt, "a-1", entity.OrderStatusPaid, 1, 3000)

		got, err := repo.ListReferralStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, []*entity.ReferralCode{code("a-1"), code("b"), code("zz")}, []*entity.ReferralCode{got[0].Code, got[1].Code, got[2].Code})
	})
}
