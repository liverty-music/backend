package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedLotteryEntryWithState stores a lottery entry of the stock's sale in
// the given state for userID with its Order of count tickets (Pending, or
// Void when Lost) with a card authorization recorded for it, and returns
// the entry's id.
func seedLotteryEntryWithState(t *testing.T, db *rdb.Database, f *stockFixture, userID string, state entity.LotteryEntryState, count int) entity.LotteryEntryID {
	t.Helper()
	return seedLotteryEntryInSale(t, db, f, f.SaleID, userID, state, count)
}

// seedLotteryEntryInSale is seedLotteryEntryWithState for an entry of the
// given sale.
func seedLotteryEntryInSale(t *testing.T, db *rdb.Database, f *stockFixture, saleID entity.TicketSaleID, userID string, state entity.LotteryEntryState, count int) entity.LotteryEntryID {
	t.Helper()
	return seedLotteryEntryWithCode(t, db, f, saleID, userID, state, count, "")
}

// seedLotteryEntryWithCode is seedLotteryEntryInSale with the Order's
// referral code, unless it is empty.
func seedLotteryEntryWithCode(t *testing.T, db *rdb.Database, f *stockFixture, saleID entity.TicketSaleID, userID string, state entity.LotteryEntryState, count int, code entity.ReferralCode) entity.LotteryEntryID {
	t.Helper()
	ctx := context.Background()
	id, orderID := entity.NewID(), entity.NewID()
	status := entity.OrderStatusPending
	if state == entity.LotteryEntryStateLost {
		status = entity.OrderStatusVoid
	}
	var referralCode any
	if code != "" {
		referralCode = string(code)
	}
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, referral_code, card_authorization_id, provider, status, amount, currency)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $8, 'JPY')`,
		orderID, userID, string(f.ID), count, referralCode, newCardAuthorizationID(db), int16(status), f.Price()*int64(count))
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `
		INSERT INTO lottery_entries (id, state, order_id, ticket_sale_id) VALUES ($1, $2, $3, $4)`,
		id, int16(state), orderID, string(saleID))
	require.NoError(t, err)
	return entity.LotteryEntryID(id)
}

func TestCheckoutRepository_GetOrCreateHeld_ReferralCode(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewCheckoutRepository(db, testOutbox)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)
	code := func(c entity.ReferralCode) *entity.ReferralCode { return &c }

	t.Run("the code is stored on the new order", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 100, 4)

		c, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, nextFan(t, db), 2, code("member-a"), at)

		require.NoError(t, err)
		assert.Equal(t, code("member-a"), c.Order.ReferralCode)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, code("member-a"), got.Order.ReferralCode)
	})

	t.Run("a resumed hold keeps the code it started with", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 100, 4)
		fan := nextFan(t, db)
		first, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, code("member-a"), at)
		require.NoError(t, err)

		again, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, code("member-b"), at.Add(time.Minute))

		require.NoError(t, err)
		assert.Equal(t, first.ID, again.ID)
		assert.Equal(t, code("member-a"), again.Order.ReferralCode)
	})

	t.Run("no code", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 100, 4)

		c, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, nextFan(t, db), 1, nil, at)

		require.NoError(t, err)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Nil(t, got.Order.ReferralCode)
	})
}

func TestOrderRepository_ListReferralStatsByEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewOrderRepository(db)
	ctx := context.Background()
	code := func(c entity.ReferralCode) *entity.ReferralCode { return &c }
	// checkoutOrder stores an Order of count tickets for amount yen with the
	// code (none when empty) in the status, through a Checkout whose status
	// follows from it.
	checkoutOrder := func(t *testing.T, f *stockFixture, refCode entity.ReferralCode, status entity.OrderStatus, count int, amount int64) {
		t.Helper()
		checkoutStatus := entity.CheckoutStatusCompleted
		if status == entity.OrderStatusVoid {
			checkoutStatus = entity.CheckoutStatusExpired
		}
		insertCheckoutOrder(t, db, f, orderSeed{count: count, amount: amount, code: refCode, status: status},
			checkoutStatus, issuedTicketsTime)
	}

	t.Run("two codes and direct purchases", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Two codes and direct purchases"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 100, 4)
		checkoutOrder(t, f, "member-a", entity.OrderStatusPaid, 2, 6500)
		checkoutOrder(t, f, "member-a", entity.OrderStatusPaid, 3, 9750)
		checkoutOrder(t, f, "", entity.OrderStatusPaid, 1, 3250)

		got, err := repo.ListReferralStatsByEvent(ctx, f.EventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: code("member-a"), OrderCount: 2, TicketCount: 5, Amount: 16250},
			{Code: nil, OrderCount: 1, TicketCount: 1, Amount: 3250},
		}, got)
	})

	t.Run("lottery win counted once its order exists", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Lottery win counted once its Order exists"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		_, err := db.Pool.Exec(ctx, `UPDATE seat_types SET price = 8000 WHERE id = $1`, string(f.SeatType.ID))
		require.NoError(t, err)
		f.SeatType.Price = 8000
		entryID := seedLotteryEntryWithCode(t, db, f, f.SaleID, seedFanUser(t, db), entity.LotteryEntryStateEntered, 2, "member-b")

		before, err := repo.ListReferralStatsByEvent(ctx, f.EventID)
		require.NoError(t, err)
		assert.Empty(t, before, "a Pending Order of an Entered entry is not counted")

		_, err = db.Pool.Exec(ctx, `UPDATE lottery_entries SET state = 2 WHERE id = $1`, string(entryID))
		require.NoError(t, err)
		_, err = db.Pool.Exec(ctx, `UPDATE orders SET status = 1, provider_payment_id = 'pi_paid_' || id, pay_at = now() WHERE id = $1`,
			string(entryOrderID(t, db, entryID)))
		require.NoError(t, err)

		after, err := repo.ListReferralStatsByEvent(ctx, f.EventID)
		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: code("member-b"), OrderCount: 1, TicketCount: 2, Amount: 16000},
		}, after)
	})

	t.Run("refunded order excluded", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Refunded order excluded"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 100, 4)
		checkoutOrder(t, f, "member-a", entity.OrderStatusRefunded, 2, 6000)
		checkoutOrder(t, f, "", entity.OrderStatusPaid, 1, 3000)

		got, err := repo.ListReferralStatsByEvent(ctx, f.EventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: nil, OrderCount: 1, TicketCount: 1, Amount: 3000},
		}, got)
	})

	t.Run("abandoned checkout not counted", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Abandoned checkout not counted"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 100, 4)
		checkoutOrder(t, f, "member-c", entity.OrderStatusVoid, 2, 6000)

		got, err := repo.ListReferralStatsByEvent(ctx, f.EventID)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("other events not counted", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "Other events not counted"
		events := seedFirstPartyEvents(t, db, "PUBLISHED", "PUBLISHED")
		this := seedStockForEvent(t, db, events[0], entity.TicketSaleMethodFirstCome, 100, 4)
		other := seedStockForEvent(t, db, events[1], entity.TicketSaleMethodFirstCome, 100, 4)
		checkoutOrder(t, this, "member-a", entity.OrderStatusPaid, 1, 3000)
		checkoutOrder(t, other, "member-a", entity.OrderStatusPaid, 2, 6000)

		got, err := repo.ListReferralStatsByEvent(ctx, events[0])

		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: code("member-a"), OrderCount: 1, TicketCount: 1, Amount: 3000},
		}, got)
	})

	t.Run("no purchases", func(t *testing.T) {
		// @spec components/entity/order/list-referral-stats-by-event "No purchases"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 100, 4)

		got, err := repo.ListReferralStatsByEvent(ctx, f.EventID)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("groups ordered by order count then code", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 100, 4)
		checkoutOrder(t, f, "zz", entity.OrderStatusPaid, 1, 3000)
		checkoutOrder(t, f, "b", entity.OrderStatusPaid, 1, 3000)
		checkoutOrder(t, f, "a-1", entity.OrderStatusPaid, 1, 3000)
		checkoutOrder(t, f, "a-1", entity.OrderStatusPaid, 1, 3000)

		got, err := repo.ListReferralStatsByEvent(ctx, f.EventID)

		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, []*entity.ReferralCode{code("a-1"), code("b"), code("zz")}, []*entity.ReferralCode{got[0].Code, got[1].Code, got[2].Code})
	})
}
