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
	"uuid"
)

// saleFan seeds a fan user with a unique email and returns its id.
func saleFan(t *testing.T) string {
	t.Helper()
	return seedUser(t, "sale-fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
}

// insertStockOrder stores an Order of count tickets of the stock for the user
// in the given status and returns its id. A Paid or Refunded Order gets its
// payment columns and a card authorization; otherwise withAuth gives it a card
// authorization.
func insertStockOrder(t *testing.T, f *stockFixture, userID string, status entity.OrderStatus, count int, withAuth bool) string {
	t.Helper()
	id := entity.NewID()
	var cardAuth, paymentID, payAt any
	if withAuth || status == entity.OrderStatusPaid || status == entity.OrderStatusRefunded {
		cardAuth = "pi_auth_" + id
	}
	if status == entity.OrderStatusPaid || status == entity.OrderStatusRefunded {
		paymentID, payAt = "pi_pay_"+id, issuedTicketsTime
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, card_authorization_id, provider,
			provider_payment_id, status, amount, currency, pay_at)
		VALUES ($1, $2, $3, $4, $5, 1, $6, $7, $8, 'JPY', $9)`,
		id, userID, string(f.ID), count, cardAuth, paymentID, int16(status), f.Price()*int64(count), payAt)
	require.NoError(t, err)
	return id
}

// insertStockCheckout stores a Checkout in the given status, holding until
// holdExpire, that names a new Order of count tickets of the stock in
// orderStatus, and returns the checkout's and the order's ids. withAuth gives
// the Order a card authorization; releaseAt, when set, records it released.
func insertStockCheckout(t *testing.T, f *stockFixture, userID string, orderStatus entity.OrderStatus, status entity.CheckoutStatus,
	count int, holdExpire time.Time, withAuth bool, releaseAt *time.Time,
) (checkoutID, orderID string) {
	t.Helper()
	orderID = insertStockOrder(t, f, userID, orderStatus, count, withAuth)
	checkoutID = entity.NewID()
	var commitAt, captureAt any
	if status == entity.CheckoutStatusCommitted || status == entity.CheckoutStatusCompleted {
		commitAt = holdExpire.Add(-time.Minute)
	}
	if status == entity.CheckoutStatusCompleted {
		captureAt = holdExpire.Add(-time.Minute)
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO checkouts (id, status, hold_expire_at, commit_at, capture_at, card_authorization_release_at, order_id, ticket_sale_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		checkoutID, int16(status), holdExpire, commitAt, captureAt, releaseAt, orderID, string(f.SaleID))
	require.NoError(t, err)
	return checkoutID, orderID
}

// insertStockEntry stores a LotteryEntry of the stock's sale in the given
// state that names a new Order of count tickets of the stock in orderStatus,
// with a card authorization, and returns the entry's and the order's ids.
func insertStockEntry(t *testing.T, f *stockFixture, userID string, orderStatus entity.OrderStatus, state entity.LotteryEntryState, count int) (entryID, orderID string) {
	t.Helper()
	orderID = insertStockOrder(t, f, userID, orderStatus, count, true)
	entryID = entity.NewID()
	_, err := testDB.Pool.Exec(context.Background(),
		`INSERT INTO lottery_entries (id, state, order_id, ticket_sale_id) VALUES ($1, $2, $3, $4)`,
		entryID, int16(state), orderID, string(f.SaleID))
	require.NoError(t, err)
	return entryID, orderID
}

// insertHolds stores Pending Orders with Held checkouts of the stock for new
// fans, one per count, each holding until holdExpire.
func insertHolds(t *testing.T, f *stockFixture, holdExpire time.Time, counts ...int) {
	t.Helper()
	for _, n := range counts {
		insertStockCheckout(t, f, saleFan(t), entity.OrderStatusPending, entity.CheckoutStatusHeld, n, holdExpire, true, nil)
	}
}

// backendBlocking reports whether any backend waits on a lock the backend pid
// holds.
func backendBlocking(t *testing.T, pid uint32) func() bool {
	t.Helper()
	return func() bool {
		var holds bool
		require.NoError(t, testDB.Pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid)))`, int32(pid)).Scan(&holds))
		return holds
	}
}

// seatTypeOf stores a seat type of the event's Series with the repository.
func seatTypeOf(t *testing.T, eventID, name string, price int64) *entity.SeatType {
	t.Helper()
	st, err := rdb.NewSeatTypeRepository(testDB).Create(context.Background(), entity.NewSeatType(seriesOf(t, eventID), name, price))
	require.NoError(t, err)
	return st
}

// queryCount runs a SELECT COUNT(*) query and returns the count.
func queryCount(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

func TestTicketStockRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketStockRepository(testDB)
	ctx := context.Background()

	t.Run("stock stored", func(t *testing.T) {
		// @spec components/entity/ticket-stock/create "Stock stored"
		eventID := seedPublishedEvent(t)
		st := seatTypeOf(t, eventID, "S席", 8000)
		stock := entity.NewTicketStock(eventID, st.ID, 150)

		got, err := repo.Create(ctx, stock)

		require.NoError(t, err)
		assert.Equal(t, stock.ID, got.ID)
		assert.Equal(t, eventID, got.EventID)
		assert.Equal(t, 150, got.Quantity)
		assert.Zero(t, got.SoldCount)
		assert.Zero(t, got.HeldCount)
		require.NotNil(t, got.SeatType)
		assert.Equal(t, st, got.SeatType)
		stored, err := repo.Get(ctx, stock.ID, saleWindowStart)
		require.NoError(t, err)
		assert.Equal(t, got, stored)
	})

	t.Run("same seat type twice", func(t *testing.T) {
		// @spec components/entity/ticket-stock/create "Same seat type twice"
		eventID := seedPublishedEvent(t)
		st := seatTypeOf(t, eventID, "S席", 8000)
		_, err := repo.Create(ctx, entity.NewTicketStock(eventID, st.ID, 150))
		require.NoError(t, err)

		_, err = repo.Create(ctx, entity.NewTicketStock(eventID, st.ID, 100))

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		assert.Equal(t, 1, queryCount(t, `SELECT COUNT(*) FROM ticket_stocks WHERE event_id = $1`, eventID))
	})

	t.Run("event of another series", func(t *testing.T) {
		// @spec components/entity/ticket-stock/create "Event of another series"
		st := seatTypeOf(t, seedPublishedEvent(t), "S席", 8000)
		other := seedPublishedEvent(t)

		_, err := repo.Create(ctx, entity.NewTicketStock(other, st.ID, 150))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Zero(t, queryCount(t, `SELECT COUNT(*) FROM ticket_stocks WHERE event_id = $1`, other))
	})

	t.Run("unknown event", func(t *testing.T) {
		// @spec components/entity/ticket-stock/create "Unknown event"
		st := seatTypeOf(t, seedPublishedEvent(t), "S席", 8000)
		missing := entity.NewID()

		_, err := repo.Create(ctx, entity.NewTicketStock(missing, st.ID, 150))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Zero(t, queryCount(t, `SELECT COUNT(*) FROM ticket_stocks WHERE seat_type_id = $1`, string(st.ID)))
	})

	t.Run("unknown seat type", func(t *testing.T) {
		eventID := seedPublishedEvent(t)

		_, err := repo.Create(ctx, entity.NewTicketStock(eventID, entity.SeatTypeID(entity.NewID()), 150))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("empty stock", func(t *testing.T) {
		eventID := seedPublishedEvent(t)
		st := seatTypeOf(t, eventID, "S席", 8000)

		_, err := repo.Create(ctx, entity.NewTicketStock(eventID, st.ID, 0))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Zero(t, queryCount(t, `SELECT COUNT(*) FROM ticket_stocks WHERE event_id = $1`, eventID))
	})
}

func TestTicketStockRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketStockRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(17 * time.Hour)

	t.Run("stock with holds", func(t *testing.T) {
		// @spec components/entity/ticket-stock/get "Stock with holds"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 10)
		setStockSold(t, f, 100)
		insertHolds(t, f, at.Add(entity.CheckoutHoldDuration), 2, 1)
		insertHolds(t, f, at, 4) // lapsed at the read time

		got, err := repo.Get(ctx, f.ID, at)

		require.NoError(t, err)
		assert.Equal(t, f.ID, got.ID)
		assert.Equal(t, f.SeatType, got.SeatType)
		assert.Equal(t, 100, got.SoldCount)
		assert.Equal(t, 3, got.HeldCount)
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/ticket-stock/get "Unknown id"
		_, err := repo.Get(ctx, entity.TicketStockID(entity.NewID()), at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// TestTicketStockRepository_Counts covers the sold and held counts every read
// aggregates from the stock's Orders (design D5).
func TestTicketStockRepository_Counts(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketStockRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(17 * time.Hour)
	holding := at.Add(entity.CheckoutHoldDuration)
	released := at.Add(-time.Hour)

	read := func(t *testing.T, f *stockFixture) *entity.TicketStock {
		t.Helper()
		got, err := repo.Get(ctx, f.ID, at)
		require.NoError(t, err)
		return got
	}

	t.Run("some sold, some held", func(t *testing.T) {
		// @spec components/entity/ticket-stock "Some sold, some held"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 10)
		setStockSold(t, f, 100)
		insertHolds(t, f, holding, 4, 2)

		got := read(t, f)

		assert.Equal(t, 100, got.SoldCount)
		assert.Equal(t, 6, got.HeldCount)
		assert.Equal(t, 44, got.Remaining())
	})

	t.Run("presale winners count as sold", func(t *testing.T) {
		// @spec components/entity/ticket-stock "Presale winners count as sold"
		f := seedStock(t, entity.TicketSaleMethodLottery, 150, 10)
		for range 12 {
			insertStockEntry(t, f, saleFan(t), entity.OrderStatusPending, entity.LotteryEntryStateWon, 10)
		}

		got := read(t, f)

		assert.Equal(t, 120, got.SoldCount)
		assert.Zero(t, got.HeldCount)
		assert.Equal(t, 30, got.Remaining())
	})

	t.Run("resold tickets do not count again", func(t *testing.T) {
		// @spec components/entity/ticket-stock "Resold tickets do not count again"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 10)
		setStockSold(t, f, 150)
		// The buyer's Order of an official resale names the stock but no
		// LotteryEntry or Checkout.
		insertStockOrder(t, f, saleFan(t), entity.OrderStatusPaid, 2, true)

		got := read(t, f)

		assert.Equal(t, 150, got.SoldCount)
		assert.Equal(t, entity.TicketStockStateSoldOut, got.State())
	})

	t.Run("each order counts by its status and its checkout or entry", func(t *testing.T) {
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 10)
		pending, paid, refunded, void := entity.OrderStatusPending, entity.OrderStatusPaid, entity.OrderStatusRefunded, entity.OrderStatusVoid
		// Held: a Pending Order whose Checkout holds at the read time.
		insertStockCheckout(t, f, saleFan(t), pending, entity.CheckoutStatusHeld, 1, holding, true, nil)
		// Neither: the hold lapsed at the read time, not yet recorded Expired.
		insertStockCheckout(t, f, saleFan(t), pending, entity.CheckoutStatusHeld, 2, at, true, nil)
		// Sold: a Pending Order whose Checkout is Committed.
		insertStockCheckout(t, f, saleFan(t), pending, entity.CheckoutStatusCommitted, 3, holding, true, nil)
		// Sold: Paid and Refunded Orders of a Completed Checkout.
		insertStockCheckout(t, f, saleFan(t), paid, entity.CheckoutStatusCompleted, 4, holding, true, nil)
		insertStockCheckout(t, f, saleFan(t), refunded, entity.CheckoutStatusCompleted, 5, holding, true, nil)
		// Sold: a Pending Order of a Won entry and a Refunded one of a Won entry.
		insertStockEntry(t, f, saleFan(t), pending, entity.LotteryEntryStateWon, 6)
		insertStockEntry(t, f, saleFan(t), refunded, entity.LotteryEntryStateWon, 7)
		// Neither: an Entered entry's Pending Order, and Void Orders of a Lost
		// entry and of Expired and Released Checkouts.
		insertStockEntry(t, f, saleFan(t), pending, entity.LotteryEntryStateEntered, 8)
		insertStockEntry(t, f, saleFan(t), void, entity.LotteryEntryStateLost, 9)
		insertStockCheckout(t, f, saleFan(t), void, entity.CheckoutStatusExpired, 10, at.Add(-time.Hour), true, &released)
		insertStockCheckout(t, f, saleFan(t), void, entity.CheckoutStatusReleased, 10, holding, true, &released)

		got := read(t, f)

		assert.Equal(t, 3+4+5+6+7, got.SoldCount)
		assert.Equal(t, 1, got.HeldCount)
	})

	t.Run("orders of another stock do not count", func(t *testing.T) {
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 10)
		other := seedStockForEvent(t, f.EventID, entity.TicketSaleMethodFirstCome, 150, 10)
		setStockSold(t, other, 20)
		insertHolds(t, other, holding, 2)

		got := read(t, f)

		assert.Zero(t, got.SoldCount)
		assert.Zero(t, got.HeldCount)
	})
}

func TestTicketStockRepository_ListByEvent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketStockRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(17 * time.Hour)

	t.Run("two seat types", func(t *testing.T) {
		// @spec components/entity/ticket-stock/list-by-event "Two seat types"
		eventID := seedPublishedEvent(t)
		a, err := repo.Create(ctx, entity.NewTicketStock(eventID, seatTypeOf(t, eventID, "A席", 7000).ID, 100))
		require.NoError(t, err)
		s, err := repo.Create(ctx, entity.NewTicketStock(eventID, seatTypeOf(t, eventID, "S席", 9000).ID, 50))
		require.NoError(t, err)
		seedPublishedEvent(t) // another event's stocks stay out
		seedStock(t, entity.TicketSaleMethodFirstCome, 10, 4)

		got, err := repo.ListByEvent(ctx, eventID, at)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, s.ID, got[0].ID)
		assert.Equal(t, "S席", got[0].SeatType.Name)
		assert.Equal(t, a.ID, got[1].ID)
		assert.Equal(t, int64(7000), got[1].SeatType.Price)
	})

	t.Run("stock with holds", func(t *testing.T) {
		// @spec components/entity/ticket-stock/list-by-event "Stock with holds"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 10)
		insertHolds(t, f, at.Add(entity.CheckoutHoldDuration), 3)

		got, err := repo.ListByEvent(ctx, f.EventID, at)

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 3, got[0].HeldCount)
		assert.Zero(t, got[0].SoldCount)
	})

	t.Run("no stock", func(t *testing.T) {
		// @spec components/entity/ticket-stock/list-by-event "No stock"
		got, err := repo.ListByEvent(ctx, seedPublishedEvent(t), at)

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestTicketStockRepository_Delete(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketStockRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(17 * time.Hour)
	released := at.Add(-time.Minute)

	stockExists := func(t *testing.T, f *stockFixture) bool {
		t.Helper()
		return rowExists(t, `SELECT 1 FROM ticket_stocks WHERE id = $1`, string(f.ID))
	}
	orderExists := func(t *testing.T, id string) bool {
		t.Helper()
		return rowExists(t, `SELECT 1 FROM orders WHERE id = $1`, id)
	}

	t.Run("stock without orders", func(t *testing.T) {
		// @spec components/entity/ticket-stock/delete "Stock without orders"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)

		require.NoError(t, repo.Delete(ctx, f.ID))

		_, err := repo.Get(ctx, f.ID, at)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.True(t, rowExists(t, `SELECT 1 FROM seat_types WHERE id = $1`, string(f.SeatType.ID)), "the seat type remains")
	})

	t.Run("stock with an abandoned checkout", func(t *testing.T) {
		// @spec components/entity/ticket-stock/delete "Stock with an abandoned checkout"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		checkoutID, orderID := insertStockCheckout(t, f, saleFan(t), entity.OrderStatusVoid, entity.CheckoutStatusExpired, 2, at, true, &released)
		// A checkout that expired before the fan authorized a card has nothing
		// to release.
		unauthorizedID, unauthorizedOrder := insertStockCheckout(t, f, saleFan(t), entity.OrderStatusVoid, entity.CheckoutStatusExpired, 1, at, false, nil)

		require.NoError(t, repo.Delete(ctx, f.ID))

		assert.False(t, stockExists(t, f), "stock")
		assert.False(t, orderExists(t, orderID), "void order")
		assert.False(t, rowExists(t, `SELECT 1 FROM checkouts WHERE id = $1`, checkoutID), "checkout")
		assert.False(t, orderExists(t, unauthorizedOrder), "void order without a card authorization")
		assert.False(t, rowExists(t, `SELECT 1 FROM checkouts WHERE id = $1`, unauthorizedID), "checkout without a card authorization")
	})

	t.Run("stock with a lost lottery entry", func(t *testing.T) {
		f := seedStock(t, entity.TicketSaleMethodLottery, 150, 4)
		entryID, orderID := insertStockEntry(t, f, saleFan(t), entity.OrderStatusVoid, entity.LotteryEntryStateLost, 2)

		require.NoError(t, repo.Delete(ctx, f.ID))

		assert.False(t, stockExists(t, f), "stock")
		assert.False(t, orderExists(t, orderID), "void order")
		assert.False(t, rowExists(t, `SELECT 1 FROM lottery_entries WHERE id = $1`, entryID), "lost entry")
	})

	refused := []struct {
		name  string
		setup func(t *testing.T, f *stockFixture) string
	}{
		{
			// @spec components/entity/ticket-stock/delete "Void order with a card authorization not yet released"
			name: "a void order whose card authorization is not yet released",
			setup: func(t *testing.T, f *stockFixture) string {
				_, orderID := insertStockCheckout(t, f, saleFan(t), entity.OrderStatusVoid, entity.CheckoutStatusExpired, 2, at, true, nil)
				return orderID
			},
		},
		{
			// @spec components/entity/ticket-stock/delete "Stock with a pending order"
			name: "a pending order of a held checkout",
			setup: func(t *testing.T, f *stockFixture) string {
				_, orderID := insertStockCheckout(t, f, saleFan(t), entity.OrderStatusPending, entity.CheckoutStatusHeld, 2, at.Add(entity.CheckoutHoldDuration), true, nil)
				return orderID
			},
		},
		{
			name: "a pending order of an entered lottery entry",
			setup: func(t *testing.T, f *stockFixture) string {
				_, orderID := insertStockEntry(t, f, saleFan(t), entity.OrderStatusPending, entity.LotteryEntryStateEntered, 2)
				return orderID
			},
		},
		{
			// @spec components/entity/ticket-stock/delete "Stock with a paid order"
			name: "a paid order",
			setup: func(t *testing.T, f *stockFixture) string {
				return string(seedPaidOrder(t, f, saleFan(t), 2, issuedTicketsTime))
			},
		},
		{
			name: "a refunded order",
			setup: func(t *testing.T, f *stockFixture) string {
				_, orderID := insertStockEntry(t, f, saleFan(t), entity.OrderStatusRefunded, entity.LotteryEntryStateWon, 2)
				return orderID
			},
		},
	}
	for _, tc := range refused {
		t.Run("refuses "+tc.name+" and removes nothing", func(t *testing.T) {
			f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
			// An abandoned checkout the delete would remove with the stock.
			_, voidOrder := insertStockCheckout(t, f, saleFan(t), entity.OrderStatusVoid, entity.CheckoutStatusExpired, 1, at, true, &released)
			blocker := tc.setup(t, f)

			err := repo.Delete(ctx, f.ID)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			assert.True(t, stockExists(t, f), "stock")
			assert.True(t, orderExists(t, blocker), "the blocking order")
			assert.True(t, orderExists(t, voidOrder), "the abandoned checkout's void order")
			assert.Equal(t, 1, queryCount(t, `SELECT COUNT(*) FROM checkouts c JOIN orders o ON o.id = c.order_id WHERE o.id = $1`, voidOrder))
		})
	}

	t.Run("refuses an order created while the delete runs", func(t *testing.T) {
		f := seedStock(t, entity.TicketSaleMethodLottery, 150, 4)
		// The application inserts its Pending Order, which takes a key-share
		// lock on the stock row, and commits only once the delete waits.
		purchase, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = purchase.Rollback(ctx) }()
		orderID := entity.NewID()
		_, err = purchase.Exec(ctx, `
			INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, provider, status, amount, currency)
			VALUES ($1, $2, $3, 1, 1, 4, 3000, 'JPY')`, orderID, saleFan(t), string(f.ID))
		require.NoError(t, err)
		var pid uint32
		require.NoError(t, purchase.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid))

		deleted := make(chan error, 1)
		go func() { deleted <- repo.Delete(ctx, f.ID) }()
		require.Eventually(t, backendBlocking(t, pid), 5*time.Second, 20*time.Millisecond, "the delete waits for the application")
		require.NoError(t, purchase.Commit(ctx))

		assert.ErrorIs(t, <-deleted, apperr.ErrFailedPrecondition)
		assert.True(t, stockExists(t, f), "stock")
		assert.True(t, orderExists(t, orderID), "the pending order")
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/ticket-stock/delete "Unknown id"
		err := repo.Delete(ctx, entity.TicketStockID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
