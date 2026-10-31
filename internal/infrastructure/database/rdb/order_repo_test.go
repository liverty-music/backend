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

// orderSeed describes an order row insertOrder stores; zero fields take the
// defaults documented on each.
type orderSeed struct {
	// user is the order's fan; a new fan when empty.
	user entity.UserID
	// count is the ticket count; 1 when 0.
	count int
	// amount is the order's amount; the stock's price times count when 0.
	amount int64
	// code is the referral code; none when empty.
	code entity.ReferralCode
	// status is the order's status; Pending when unspecified.
	status entity.OrderStatus
	// payAt is the pay time of a Paid or Refunded order; issuedTicketsTime
	// when zero.
	payAt time.Time
}

// insertOrder stores an order of the stock as s describes it, with a card
// authorization "pi_auth_<id>", and the captured payment a Paid or Refunded
// order needs, and returns its id. Its source is stored by the caller.
func insertOrder(t *testing.T, f *stockFixture, s orderSeed) entity.OrderID {
	t.Helper()
	if s.user == "" {
		s.user = entity.UserID(seedFanUser(t))
	}
	if s.count == 0 {
		s.count = 1
	}
	if s.amount == 0 {
		s.amount = f.Price() * int64(s.count)
	}
	if s.status == entity.OrderStatusUnspecified {
		s.status = entity.OrderStatusPending
	}
	if s.payAt.IsZero() {
		s.payAt = issuedTicketsTime
	}
	id := entity.NewID()
	var paymentID, payAt, code any
	if s.status == entity.OrderStatusPaid || s.status == entity.OrderStatusRefunded {
		paymentID, payAt = "pi_"+id, s.payAt
	}
	if s.code != "" {
		code = string(s.code)
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, referral_code, card_authorization_id, provider,
			provider_payment_id, status, amount, currency, pay_at)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $8, $9, 'JPY', $10)`,
		id, string(s.user), string(f.ID), s.count, code, "pi_auth_"+id, paymentID, int16(s.status), s.amount, payAt)
	require.NoError(t, err)
	return entity.OrderID(id)
}

// insertCheckoutOrder stores an order of the stock as s describes it and the
// checkout of f's sale that names it, in the given status and held until
// holdExpire, with the commit and capture times that status requires, and
// returns the checkout's id.
func insertCheckoutOrder(t *testing.T, f *stockFixture, s orderSeed, status entity.CheckoutStatus, holdExpire time.Time) entity.CheckoutID {
	t.Helper()
	orderID := insertOrder(t, f, s)
	id := entity.NewID()
	var commitAt, captureAt any
	switch status {
	case entity.CheckoutStatusCommitted:
		commitAt = holdExpire.Add(-time.Minute)
	case entity.CheckoutStatusCompleted:
		commitAt, captureAt = holdExpire.Add(-time.Minute), holdExpire
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO checkouts (id, order_id, ticket_sale_id, status, hold_expire_at, commit_at, capture_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, string(orderID), string(f.SaleID), int16(status), holdExpire, commitAt, captureAt)
	require.NoError(t, err)
	return entity.CheckoutID(id)
}

// seedCheckoutOrder stores an Order of 1 ticket of the stock for a new fan,
// in the given status, paid at payAt when Paid or Refunded, bought through a
// Checkout of the stock's sale whose status follows from the Order's:
// Completed for Paid and Refunded, Committed for Pending, Expired for Void.
// It returns the Order's id.
func seedCheckoutOrder(t *testing.T, f *stockFixture, status entity.OrderStatus, payAt time.Time) entity.OrderID {
	t.Helper()
	checkoutStatus := map[entity.OrderStatus]entity.CheckoutStatus{
		entity.OrderStatusPaid:     entity.CheckoutStatusCompleted,
		entity.OrderStatusRefunded: entity.CheckoutStatusCompleted,
		entity.OrderStatusPending:  entity.CheckoutStatusCommitted,
		entity.OrderStatusVoid:     entity.CheckoutStatusExpired,
	}[status]
	require.NotZero(t, checkoutStatus, "unsupported order status %v", status)
	id := insertCheckoutOrder(t, f, orderSeed{status: status, payAt: payAt}, checkoutStatus, payAt)
	var orderID string
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		`SELECT order_id FROM checkouts WHERE id = $1`, string(id)).Scan(&orderID))
	return entity.OrderID(orderID)
}

// entryOrderID returns the id of the order the lottery entry names.
func entryOrderID(t *testing.T, id entity.LotteryEntryID) entity.OrderID {
	t.Helper()
	var orderID string
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		`SELECT order_id FROM lottery_entries WHERE id = $1`, string(id)).Scan(&orderID))
	return entity.OrderID(orderID)
}

// paidOrderEntryID returns the id of the lottery entry that names the order.
func paidOrderEntryID(t *testing.T, id entity.OrderID) entity.LotteryEntryID {
	t.Helper()
	var entryID string
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		`SELECT id FROM lottery_entries WHERE order_id = $1`, string(id)).Scan(&entryID))
	return entity.LotteryEntryID(entryID)
}

// orderIDs returns the ids of the orders in their order.
func orderIDs(orders []*entity.Order) []entity.OrderID {
	ids := make([]entity.OrderID, 0, len(orders))
	for _, o := range orders {
		ids = append(ids, o.ID)
	}
	return ids
}

func TestOrderRepository_ListByUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)

	t.Run("paid and refunded orders", func(t *testing.T) {
		// @spec components/entity/order/list-by-user "Paid and refunded orders"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
		userID := nextFan(t)
		paid := insertCheckoutOrder(t, f, orderSeed{user: userID, status: entity.OrderStatusPaid, payAt: issuedTicketsTime},
			entity.CheckoutStatusCompleted, issuedTicketsTime)
		refunded := seedPaidOrder(t, f, string(userID), 1, issuedTicketsTime.Add(time.Hour))
		_, err := testDB.Pool.Exec(ctx, `UPDATE orders SET status = 2 WHERE id = $1`, string(refunded))
		require.NoError(t, err)
		seedPaidOrder(t, f, seedFanUser(t), 1, issuedTicketsTime) // another user's

		got, err := repo.ListByUser(ctx, userID)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, paid, got[0].CheckoutID)
		assert.Equal(t, entity.OrderStatusPaid, got[0].Status)
		assert.Equal(t, refunded, got[1].ID)
		assert.Equal(t, entity.OrderStatusRefunded, got[1].Status)
		for _, o := range got {
			assert.Equal(t, userID, o.UserID)
		}
	})

	t.Run("applications not bought", func(t *testing.T) {
		// @spec components/entity/order/list-by-user "Applications not bought"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		userID := nextFan(t)
		seedLotteryEntryWithState(t, f, string(userID), entity.LotteryEntryStateEntered, 2)
		insertCheckoutOrder(t, f, orderSeed{user: userID, status: entity.OrderStatusVoid},
			entity.CheckoutStatusExpired, saleWindowStart)

		got, err := repo.ListByUser(ctx, userID)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("no orders", func(t *testing.T) {
		// @spec components/entity/order/list-by-user "No orders"
		got, err := repo.ListByUser(ctx, entity.UserID(entity.NewID()))

		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NotNil(t, got)
	})
}

func TestOrderRepository_GetByLotteryEntryID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)

	t.Run("issued entry", func(t *testing.T) {
		// @spec components/entity/order/get-by-lottery-entry-id "Issued entry"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		orderID := seedPaidOrder(t, f, seedFanUser(t), 2, issuedTicketsTime)
		entryID := paidOrderEntryID(t, orderID)

		got, err := repo.GetByLotteryEntryID(ctx, entryID)

		require.NoError(t, err)
		assert.Equal(t, orderID, got.ID)
		assert.Equal(t, entity.OrderStatusPaid, got.Status)
		assert.Equal(t, entryID, got.LotteryEntryID)
		assert.Empty(t, got.CheckoutID)
	})

	t.Run("not yet issued", func(t *testing.T) {
		// @spec components/entity/order/get-by-lottery-entry-id "Not yet issued"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		entryID := seedLotteryEntryWithState(t, f, seedFanUser(t), entity.LotteryEntryStateWon, 2)

		got, err := repo.GetByLotteryEntryID(ctx, entryID)

		require.NoError(t, err)
		assert.Equal(t, entryOrderID(t, entryID), got.ID)
		assert.Equal(t, entity.OrderStatusPending, got.Status)
		assert.Equal(t, entryID, got.LotteryEntryID)
		assert.Equal(t, 2, got.TicketCount)
		assert.Nil(t, got.PayTime)
		assert.Empty(t, got.Payment.ProviderPaymentID)
	})

	t.Run("unknown entry", func(t *testing.T) {
		// @spec components/entity/order/get-by-lottery-entry-id "Unknown entry"
		_, err := repo.GetByLotteryEntryID(ctx, entity.LotteryEntryID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestOrderRepository_GetByCheckoutID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)
	checkouts := rdb.NewCheckoutRepository(testDB, testOutbox)
	issuance := rdb.NewIssuanceRepository(testDB)
	start := saleWindowStart.Add(time.Hour)

	t.Run("paid checkout", func(t *testing.T) {
		// @spec components/entity/order/get-by-checkout-id "Paid checkout"
		c := completedCheckout(t, checkouts, issuance, start)

		got, err := repo.GetByCheckoutID(ctx, c.ID)

		require.NoError(t, err)
		assert.Equal(t, c.OrderID, got.ID)
		assert.Equal(t, entity.OrderStatusPaid, got.Status)
		assert.Equal(t, c.ID, got.CheckoutID)
		assert.Equal(t, c.Order.UserID, got.UserID)
		assert.Empty(t, got.LotteryEntryID)
	})

	t.Run("checkout not paid", func(t *testing.T) {
		// @spec components/entity/order/get-by-checkout-id "Checkout not paid"
		c := heldCheckout(t, checkouts, 1, start)

		got, err := repo.GetByCheckoutID(ctx, c.ID)

		require.NoError(t, err)
		assert.Equal(t, c.OrderID, got.ID)
		assert.Equal(t, entity.OrderStatusPending, got.Status)
		assert.Equal(t, c.ID, got.CheckoutID)
	})

	t.Run("unknown checkout", func(t *testing.T) {
		// @spec components/entity/order/get-by-checkout-id "Unknown checkout"
		_, err := repo.GetByCheckoutID(ctx, entity.CheckoutID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestOrderRepository_ListByEvent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)

	t.Run("paid and refunded orders", func(t *testing.T) {
		// @spec components/entity/order/list-by-event "Paid and refunded orders"
		firstCome := seedStock(t, entity.TicketSaleMethodFirstCome, 10, 4)
		lottery := seedStockForEvent(t, firstCome.EventID, entity.TicketSaleMethodLottery, 10, 4)
		refunded := seedPaidOrder(t, lottery, seedFanUser(t), 1, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
		_, err := testDB.Pool.Exec(ctx, `UPDATE orders SET status = 2 WHERE id = $1`, string(refunded))
		require.NoError(t, err)
		paid := seedCheckoutOrder(t, firstCome, entity.OrderStatusPaid, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))

		got, err := repo.ListByEvent(ctx, firstCome.EventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.OrderID{paid, refunded}, orderIDs(got))
		require.Len(t, got, 2)
		assert.Equal(t, entity.OrderStatusPaid, got[0].Status)
		assert.Equal(t, entity.OrderStatusRefunded, got[1].Status)
		assert.NotEmpty(t, got[1].LotteryEntryID)
	})

	t.Run("pending and void orders", func(t *testing.T) {
		// @spec components/entity/order/list-by-event "Pending and void orders"
		lottery := seedStock(t, entity.TicketSaleMethodLottery, 10, 4)
		firstCome := seedStockForEvent(t, lottery.EventID, entity.TicketSaleMethodFirstCome, 10, 4)
		seedLotteryEntryWithState(t, lottery, seedFanUser(t), entity.LotteryEntryStateEntered, 2)
		seedCheckoutOrder(t, firstCome, entity.OrderStatusVoid, saleWindowStart)

		got, err := repo.ListByEvent(ctx, lottery.EventID)

		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NotNil(t, got)
	})

	t.Run("orders of another event", func(t *testing.T) {
		// @spec components/entity/order/list-by-event "Orders of another event"
		events := seedFirstPartyEvents(t, "PUBLISHED", "PUBLISHED")
		other := seedStockForEvent(t, events[1], entity.TicketSaleMethodFirstCome, 10, 4)
		seedStockForEvent(t, events[0], entity.TicketSaleMethodFirstCome, 10, 4)
		seedCheckoutOrder(t, other, entity.OrderStatusPaid, issuedTicketsTime)

		got, err := repo.ListByEvent(ctx, events[0])

		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NotNil(t, got)
	})

	t.Run("no event given", func(t *testing.T) {
		// @spec components/entity/order/list-by-event "No event given"
		_, err := repo.ListByEvent(ctx, "")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestOrderRepository_GetEventID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)

	t.Run("order from a checkout", func(t *testing.T) {
		// @spec components/entity/order/get-event-id "Order from a checkout"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 10, 4)
		orderID := seedCheckoutOrder(t, f, entity.OrderStatusPaid, issuedTicketsTime)

		got, err := repo.GetEventID(ctx, orderID)

		require.NoError(t, err)
		assert.Equal(t, f.EventID, got)
	})

	t.Run("order from a won lottery entry", func(t *testing.T) {
		// @spec components/entity/order/get-event-id "Order from a won lottery entry"
		f := seedStock(t, entity.TicketSaleMethodLottery, 10, 4)
		entryID := seedLotteryEntryWithState(t, f, seedFanUser(t), entity.LotteryEntryStateWon, 1)

		got, err := repo.GetEventID(ctx, entryOrderID(t, entryID))

		require.NoError(t, err)
		assert.Equal(t, f.EventID, got)
	})

	t.Run("unknown order", func(t *testing.T) {
		// @spec components/entity/order/get-event-id "Unknown order"
		_, err := repo.GetEventID(ctx, entity.OrderID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestOrderRepository_GetHeldForEvent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)
	checkouts := rdb.NewCheckoutRepository(testDB, testOutbox)
	at := saleWindowStart.Add(time.Hour)
	seatType := func(f *stockFixture) *entity.SeatTypeID { return &f.SeatType.ID }

	t.Run("paid and pending orders", func(t *testing.T) {
		// @spec components/entity/order/get-held-for-event "Paid and pending orders"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
		fan := nextFan(t)
		seedIssuedTicketsOf(t, f, string(fan), 2)
		_, err := checkouts.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 1, nil, at)
		require.NoError(t, err)

		got, err := repo.GetHeldForEvent(ctx, fan, f.EventID, nil, at)

		require.NoError(t, err)
		assert.Equal(t, entity.HeldTickets{Count: 3, SeatTypeID: seatType(f)}, got)
	})

	t.Run("entered lottery entry", func(t *testing.T) {
		// @spec components/entity/order/get-held-for-event "Entered lottery entry"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		fan := nextFan(t)
		seedLotteryEntryWithState(t, f, string(fan), entity.LotteryEntryStateEntered, 2)

		got, err := repo.GetHeldForEvent(ctx, fan, f.EventID, nil, at)

		require.NoError(t, err)
		assert.Equal(t, entity.HeldTickets{Count: 2, SeatTypeID: seatType(f)}, got)
	})

	t.Run("resold ticket", func(t *testing.T) {
		// @spec components/entity/order/get-held-for-event "Resold ticket"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
		fan := nextFan(t)
		tickets := seedIssuedTicketsOf(t, f, string(fan), 2)
		voidTickets(t, tickets[0])

		got, err := repo.GetHeldForEvent(ctx, fan, f.EventID, nil, at)

		require.NoError(t, err)
		assert.Equal(t, entity.HeldTickets{Count: 1, SeatTypeID: seatType(f)}, got)
	})

	t.Run("every ticket resold", func(t *testing.T) {
		// @spec components/entity/order/get-held-for-event "Every ticket resold"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
		fan := nextFan(t)
		voidTickets(t, seedIssuedTicketsOf(t, f, string(fan), 2)...)

		got, err := repo.GetHeldForEvent(ctx, fan, f.EventID, nil, at)

		require.NoError(t, err)
		assert.Equal(t, entity.HeldTickets{}, got)
	})

	t.Run("void order", func(t *testing.T) {
		// @spec components/entity/order/get-held-for-event "Void order"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
		fan := nextFan(t)
		insertCheckoutOrder(t, f, orderSeed{user: fan, count: 2, status: entity.OrderStatusVoid}, entity.CheckoutStatusExpired, at)

		got, err := repo.GetHeldForEvent(ctx, fan, f.EventID, nil, at)

		require.NoError(t, err)
		assert.Equal(t, entity.HeldTickets{}, got)
	})

	t.Run("another event", func(t *testing.T) {
		// @spec components/entity/order/get-held-for-event "Another event"
		events := seedFirstPartyEvents(t, "PUBLISHED", "PUBLISHED")
		seedStockForEvent(t, events[0], entity.TicketSaleMethodFirstCome, 100, 4)
		other := seedStockForEvent(t, events[1], entity.TicketSaleMethodFirstCome, 100, 4)
		fan := nextFan(t)
		seedIssuedTicketsOf(t, other, string(fan), 2)

		got, err := repo.GetHeldForEvent(ctx, fan, events[0], nil, at)

		require.NoError(t, err)
		assert.Equal(t, entity.HeldTickets{}, got)
	})

	t.Run("holding checkout left out", func(t *testing.T) {
		// @spec components/entity/order/get-held-for-event "Holding checkout left out"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
		fan := nextFan(t)
		c, err := checkouts.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)

		got, err := repo.GetHeldForEvent(ctx, fan, f.EventID, &c.OrderID, at)

		require.NoError(t, err)
		assert.Equal(t, entity.HeldTickets{}, got)
		counted, err := repo.GetHeldForEvent(ctx, fan, f.EventID, nil, at)
		require.NoError(t, err)
		assert.Equal(t, 2, counted.Count, "counted when not left out")
	})

	t.Run("lapsed hold", func(t *testing.T) {
		// @spec components/entity/order/get-held-for-event "Lapsed hold"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
		fan := nextFan(t)
		c, err := checkouts.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)

		got, err := repo.GetHeldForEvent(ctx, fan, f.EventID, nil, c.HoldExpireTime.Add(time.Minute))

		require.NoError(t, err)
		assert.Equal(t, entity.HeldTickets{}, got)
	})

	t.Run("committed checkout and refunded order", func(t *testing.T) {
		// A Committed checkout still counts; a Refunded order does not.
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
		fan := nextFan(t)
		insertCheckoutOrder(t, f, orderSeed{user: fan, count: 2, status: entity.OrderStatusPending}, entity.CheckoutStatusCommitted, at)
		refunded := seedPaidOrder(t, f, string(fan), 2, issuedTicketsTime)
		_, err := testDB.Pool.Exec(ctx, `UPDATE orders SET status = 2 WHERE id = $1`, string(refunded))
		require.NoError(t, err)

		got, err := repo.GetHeldForEvent(ctx, fan, f.EventID, nil, at.Add(time.Hour))

		require.NoError(t, err)
		assert.Equal(t, entity.HeldTickets{Count: 2, SeatTypeID: seatType(f)}, got)
	})
}

// voidTickets marks the tickets Voided, as a resale of them does.
func voidTickets(t *testing.T, ids ...entity.TicketID) {
	t.Helper()
	for _, id := range ids {
		_, err := testDB.Pool.Exec(context.Background(), `UPDATE tickets SET status = 2 WHERE id = $1`, string(id))
		require.NoError(t, err)
	}
}
