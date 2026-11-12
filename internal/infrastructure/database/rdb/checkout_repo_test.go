package rdb_test

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFan seeds a user and returns its id; n keeps emails unique per test.
func newFan(t *testing.T, db *rdb.Database, n string) entity.UserID {
	t.Helper()
	return entity.UserID(seedUser(t, db, "fan-"+n, "fan-"+n+"@example.com", "ext-fan-"+n))
}

// fanCounter makes fan names unique across the tests of the package.
var fanCounter struct {
	sync.Mutex
	n int
}

// nextFan seeds a new user with a unique name and returns its id.
func nextFan(t *testing.T, db *rdb.Database) entity.UserID {
	t.Helper()
	fanCounter.Lock()
	fanCounter.n++
	n := fanCounter.n
	fanCounter.Unlock()
	return newFan(t, db, fmt.Sprintf("checkout-%d-%s", n, entity.NewID()[:8]))
}

// stockCounts returns the stock's sold and held counts at at, as
// TicketStockRepository.Get aggregates them.
func stockCounts(t *testing.T, db *rdb.Database, id entity.TicketStockID, at time.Time) (sold, held int) {
	t.Helper()
	s, err := rdb.NewTicketStockRepository(db).Get(context.Background(), id, at)
	require.NoError(t, err)
	return s.SoldCount, s.HeldCount
}

// orderStatusOf returns the stored status of the order.
func orderStatusOf(t *testing.T, db *rdb.Database, id entity.OrderID) entity.OrderStatus {
	t.Helper()
	var status int16
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT status FROM orders WHERE id = $1`, string(id)).Scan(&status))
	return entity.OrderStatus(status)
}

func TestCheckoutRepository_GetOrCreateHeld(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewCheckoutRepository(db, testOutbox)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)

	t.Run("first start", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "First start"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		setStockSold(t, db, f, 106)
		fan := nextFan(t, db)

		c, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)

		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, c.Status)
		assert.Equal(t, f.SaleID, c.TicketSaleID)
		assert.Equal(t, at.Add(15*time.Minute), c.HoldExpireTime)
		require.NotNil(t, c.Order)
		assert.Equal(t, c.OrderID, c.Order.ID)
		assert.Equal(t, entity.OrderStatusPending, c.Order.Status)
		assert.Equal(t, fan, c.Order.UserID)
		assert.Equal(t, f.ID, c.Order.TicketStockID)
		assert.Equal(t, 2, c.Order.TicketCount)
		assert.Equal(t, 2*f.Price(), c.Order.Amount)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
		assert.Equal(t, c.ID, got.Order.CheckoutID)
		_, held := stockCounts(t, db, f.ID, at)
		assert.Equal(t, 2, held)
	})

	t.Run("double tap", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Double tap"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)

		var wg sync.WaitGroup
		results := make([]*entity.Checkout, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { results[i], errs[i] = repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at) })
		}
		wg.Wait()

		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, results[0].ID, results[1].ID)
		assert.Equal(t, results[0].OrderID, results[1].OrderID)
		_, held := stockCounts(t, db, f.ID, at)
		assert.Equal(t, 2, held)
	})

	t.Run("count changed", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Count changed"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)
		first, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)

		second, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 3, nil, at.Add(time.Minute))

		require.NoError(t, err)
		assert.NotEqual(t, first.ID, second.ID)
		assert.Equal(t, 3, second.Order.TicketCount)
		assert.Equal(t, entity.CheckoutStatusHeld, second.Status)
		replaced, err := repo.Get(ctx, first.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusReleased, replaced.Status)
		assert.Equal(t, entity.OrderStatusVoid, replaced.Order.Status)
		_, held := stockCounts(t, db, f.ID, at.Add(time.Minute))
		assert.Equal(t, 3, held)
	})

	t.Run("count raised when stock is short", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Count raised when stock is short"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 10, 4)
		setStockSold(t, db, f, 7)
		fan := nextFan(t, db)
		held, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)

		_, err = repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 4, nil, at)

		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
		still, err := repo.Get(ctx, held.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, still.Status)
		assert.Equal(t, entity.OrderStatusPending, still.Order.Status)
		_, heldCount := stockCounts(t, db, f.ID, at)
		assert.Equal(t, 2, heldCount, "the fan still holds 2 tickets")
	})

	t.Run("two fans, last ticket", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Two fans, last ticket"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 10, 4)
		setStockSold(t, db, f, 9)
		fans := []entity.UserID{nextFan(t, db), nextFan(t, db)}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { _, errs[i] = repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fans[i], 1, nil, at) })
		}
		wg.Wait()

		won := 0
		for _, err := range errs {
			if err == nil {
				won++
				continue
			}
			assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
		}
		assert.Equal(t, 1, won)
		_, held := stockCounts(t, db, f.ID, at)
		assert.Equal(t, 1, held)
	})

	t.Run("over the per-account limit", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Over the per-account limit"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)
		seedIssuedTicketsOf(t, db, f, string(fan), 3)

		_, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, held := stockCounts(t, db, f.ID, at)
		assert.Equal(t, 0, held, "nothing is held")

		// One more ticket still fits the limit of 4.
		_, err = repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 1, nil, at)
		assert.NoError(t, err)
	})

	t.Run("presale winner in the general sale", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Presale winner in the general sale"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		presale := seedSale(t, db, f.EventID, entity.TicketSaleMethodLottery,
			saleWindowStart.Add(-10*24*time.Hour), saleWindowStart.Add(-time.Hour))
		fan := nextFan(t, db)
		seedLotteryEntryInSale(t, db, f, presale, string(fan), entity.LotteryEntryStateWon, 4)

		_, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 1, nil, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, held := stockCounts(t, db, f.ID, at)
		assert.Equal(t, 0, held, "nothing is held")
	})

	t.Run("another seat type", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Another seat type"
		sSeats := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		aSeats := seedStockForEvent(t, db, sSeats.EventID, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)
		seedIssuedTicketsOf(t, db, sSeats, string(fan), 2)

		_, err := repo.GetOrCreateHeld(ctx, aSeats.ID, aSeats.SaleID, fan, 1, nil, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, held := stockCounts(t, db, aSeats.ID, at)
		assert.Equal(t, 0, held, "nothing is held")
	})

	t.Run("committed checkout counts until issued", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)
		bought, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 4, nil, at)
		require.NoError(t, err)
		outcome, err := repo.Commit(ctx, bought.ID, at)
		require.NoError(t, err)
		require.Equal(t, entity.CommitOutcomeCommitted, outcome)

		_, err = repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 1, nil, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("issued checkout counts once", func(t *testing.T) {
		// A Completed checkout's tickets count as its Paid Order's Issued
		// tickets only.
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		c := completedCheckoutOf(t, db, repo, rdb.NewIssuanceRepository(db, testOutbox), f, nextFan(t, db), 2, at)

		_, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, c.Order.UserID, 2, nil, at)

		assert.NoError(t, err)
	})

	t.Run("a lapsed hold is expired when the fan starts again", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)
		lapsed, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)

		fresh, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at.Add(16*time.Minute))

		require.NoError(t, err)
		assert.NotEqual(t, lapsed.ID, fresh.ID)
		old, err := repo.Get(ctx, lapsed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusExpired, old.Status)
		assert.Equal(t, entity.OrderStatusVoid, old.Order.Status)
	})

	t.Run("unknown ticket stock", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Unknown ticket stock"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		_, err := repo.GetOrCreateHeld(ctx, entity.TicketStockID(entity.NewID()), f.SaleID, nextFan(t, db), 1, nil, at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("unknown user", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		_, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, entity.UserID(entity.NewID()), 1, nil, at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// checkoutAnnouncements returns the announcements of subject recorded after
// mark that carry the Checkout id.
func checkoutAnnouncements(t *testing.T, db *rdb.Database, mark int64, subject string, id entity.CheckoutID) []*entityv1.Checkout {
	t.Helper()
	return announcedSince(t, db, mark, subject, func() *entityv1.Checkout { return &entityv1.Checkout{} },
		func(c *entityv1.Checkout) bool { return c.GetId().GetValue() == string(id) })
}

func TestCheckoutRepository_GetOrCreateHeldAnnounces(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewCheckoutRepository(db, testOutbox)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)

	t.Run("count changed", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Count changed"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)
		mark := outboxMark(t, db)
		first, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)
		second, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 3, nil, at.Add(time.Minute))
		require.NoError(t, err)

		released := checkoutAnnouncements(t, db, mark, entity.SubjectCheckoutReleased, first.ID)
		require.Len(t, released, 1, "the 2-ticket Checkout is announced as released")
		assert.Equal(t, entityv1.CheckoutStatus_CHECKOUT_STATUS_RELEASED, released[0].GetStatus())
		assert.Equal(t, string(first.OrderID), released[0].GetOrderId().GetValue())
		held := checkoutAnnouncements(t, db, mark, entity.SubjectCheckoutHeld, second.ID)
		require.Len(t, held, 1, "the 3-ticket Checkout is announced as held")
		assert.Equal(t, entityv1.CheckoutStatus_CHECKOUT_STATUS_HELD, held[0].GetStatus())
		assert.True(t, second.HoldExpireTime.Equal(held[0].GetHoldExpireTime().AsTime()))
	})

	t.Run("lapsed hold replaced", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)
		lapsed, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)
		mark := outboxMark(t, db)
		_, err = repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, lapsed.HoldExpireTime.Add(time.Minute))
		require.NoError(t, err)

		expired := checkoutAnnouncements(t, db, mark, entity.SubjectCheckoutExpired, lapsed.ID)
		require.Len(t, expired, 1, "the lapsed hold is announced as expired")
		assert.Equal(t, entityv1.CheckoutStatus_CHECKOUT_STATUS_EXPIRED, expired[0].GetStatus())
	})

	t.Run("same hold resumed", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Same hold resumed"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)
		first, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)
		mark := outboxMark(t, db)
		again, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at.Add(time.Minute))
		require.NoError(t, err)

		require.Equal(t, first.ID, again.ID)
		for _, subject := range []string{entity.SubjectCheckoutHeld, entity.SubjectCheckoutReleased, entity.SubjectCheckoutExpired} {
			assert.Empty(t, checkoutAnnouncements(t, db, mark, subject, first.ID), subject)
		}
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Announcement cannot be recorded"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t, db)
		earlier, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)

		_, err = rdb.NewCheckoutRepository(db, failingOutbox{}).GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 3, nil, at.Add(time.Minute))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		got, err := repo.Get(ctx, earlier.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, got.Status, "the earlier hold stays Held")
		_, heldCount := stockCounts(t, db, f.ID, at.Add(time.Minute))
		assert.Equal(t, 2, heldCount, "no Checkout is created")
	})
}

// heldCheckout starts a checkout of a new fan for count tickets on a fresh
// first-come stock of 150 tickets with a per-account limit of 10.
func heldCheckout(t *testing.T, db *rdb.Database, repo *rdb.CheckoutRepository, count int, at time.Time) *entity.Checkout {
	t.Helper()
	f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 10)
	c, err := repo.GetOrCreateHeld(context.Background(), f.ID, f.SaleID, nextFan(t, db), count, nil, at)
	require.NoError(t, err)
	return c
}

func TestCheckoutRepository_Lifecycle(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewCheckoutRepository(db, testOutbox)
	issuance := rdb.NewIssuanceRepository(db, testOutbox)
	ctx := context.Background()
	start := saleWindowStart.Add(time.Hour)
	expiry := start.Add(15 * time.Minute)

	t.Run("get", func(t *testing.T) {
		// @spec components/entity/checkout/get "Unknown checkout"
		_, err := repo.Get(ctx, entity.CheckoutID(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)

		// @spec components/entity/checkout/get "Completed checkout"
		c := completedCheckout(t, db, repo, issuance, start)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCompleted, got.Status)
		assert.Equal(t, entity.OrderStatusPaid, got.Order.Status)
	})

	t.Run("set card authorization", func(t *testing.T) {
		c := heldCheckout(t, db, repo, 2, start)
		auth1 := newCardAuthorizationID(db)

		// @spec components/entity/checkout/set-card-authorization "First card authorization"
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, auth1))
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, auth1, got.Order.CardAuthorizationID)
		assert.Equal(t, c.Status, got.Status)
		assert.Equal(t, c.Order.TicketCount, got.Order.TicketCount)
		assert.Equal(t, c.Order.Amount, got.Order.Amount)
		assert.Equal(t, c.Order.Status, got.Order.Status)
		assert.True(t, c.HoldExpireTime.Equal(got.HoldExpireTime))

		// @spec components/entity/checkout/set-card-authorization "Same card authorization on retry"
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, auth1))
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, auth1, got.Order.CardAuthorizationID)

		// @spec components/entity/checkout/set-card-authorization "Another card authorization"
		assert.ErrorIs(t, repo.SetCardAuthorization(ctx, c.ID, newCardAuthorizationID(db)), apperr.ErrFailedPrecondition)
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, auth1, got.Order.CardAuthorizationID, "nothing changes")

		// @spec components/entity/checkout/set-card-authorization "Expired checkout"
		lapsed := heldCheckout(t, db, repo, 1, start)
		_, err = repo.Release(ctx, lapsed.ID, expiry)
		require.NoError(t, err)
		assert.ErrorIs(t, repo.SetCardAuthorization(ctx, lapsed.ID, newCardAuthorizationID(db)), apperr.ErrFailedPrecondition)
		got, err = repo.Get(ctx, lapsed.ID)
		require.NoError(t, err)
		assert.Empty(t, got.Order.CardAuthorizationID)

		assert.ErrorIs(t, repo.SetCardAuthorization(ctx, entity.CheckoutID(entity.NewID()), newCardAuthorizationID(db)), apperr.ErrNotFound)
	})

	t.Run("commit", func(t *testing.T) {
		// @spec components/entity/checkout/commit "Within the hold"
		c := heldCheckout(t, db, repo, 2, start)
		outcome, err := repo.Commit(ctx, c.ID, start.Add(5*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
		assert.Equal(t, start.Add(5*time.Minute), got.CommitTime.UTC())
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
		sold, held := stockCounts(t, db, c.Order.TicketStockID, start.Add(5*time.Minute))
		assert.Equal(t, 2, sold)
		assert.Equal(t, 0, held)

		// @spec components/entity/checkout/commit "Repeated commit"
		outcome, err = repo.Commit(ctx, c.ID, start.Add(6*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		again, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, start.Add(5*time.Minute), again.CommitTime.UTC(), "the commit time is kept")
		sold, _ = stockCounts(t, db, c.Order.TicketStockID, start.Add(6*time.Minute))
		assert.Equal(t, 2, sold, "a repeated commit adds nothing")

		// @spec components/entity/checkout/commit "Hold lapsed"
		lapsed := heldCheckout(t, db, repo, 2, start)
		outcome, err = repo.Commit(ctx, lapsed.ID, expiry)
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeNotHeld, outcome)
		got, err = repo.Get(ctx, lapsed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, got.Status)
		assert.Nil(t, got.CommitTime)

		// Released and Expired checkouts report NotHeld too.
		_, err = repo.Release(ctx, lapsed.ID, expiry)
		require.NoError(t, err)
		outcome, err = repo.Commit(ctx, lapsed.ID, expiry)
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeNotHeld, outcome)

		_, err = repo.Commit(ctx, entity.CheckoutID(entity.NewID()), start)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("last tickets committed", func(t *testing.T) {
		// @spec components/entity/checkout/commit "Last tickets committed"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 4)
		setStockSold(t, db, f, 148)
		startedAt := time.Date(2026, 11, 2, 20, 5, 0, 0, time.UTC)
		c, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, nextFan(t, db), 2, nil, startedAt)
		require.NoError(t, err)
		committedAt := time.Date(2026, 11, 2, 20, 15, 0, 0, time.UTC)

		outcome, err := repo.Commit(ctx, c.ID, committedAt)

		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		stock, err := rdb.NewTicketStockRepository(db).Get(ctx, f.ID, committedAt)
		require.NoError(t, err)
		assert.Equal(t, 150, stock.SoldCount)
		assert.Equal(t, entity.TicketStockStateSoldOut, stock.State())
		resaleStart := committedAt.Add(14 * 24 * time.Hour)
		assert.True(t, stock.IsResaleOpenAt(committedAt, &entity.Event{StartTime: &resaleStart}))
	})

	t.Run("release announces", func(t *testing.T) {
		checkoutsOf := func(mark int64, id entity.CheckoutID) []*entityv1.Checkout {
			return announcedSince(t, db, mark, entity.SubjectCheckoutExpired, func() *entityv1.Checkout { return &entityv1.Checkout{} },
				func(c *entityv1.Checkout) bool { return c.GetId().GetValue() == string(id) })
		}

		// @spec components/entity/checkout/release "Hold lapsed"
		lapsed := heldCheckout(t, db, repo, 1, start)
		mark := outboxMark(t, db)
		changed, err := repo.Release(ctx, lapsed.ID, expiry.Add(time.Minute))
		require.NoError(t, err)
		require.True(t, changed)
		got := checkoutsOf(mark, lapsed.ID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.CheckoutStatus_CHECKOUT_STATUS_EXPIRED, got[0].GetStatus())

		// @spec components/entity/checkout/release "Nothing to release"
		holding := heldCheckout(t, db, repo, 1, start)
		mark = outboxMark(t, db)
		_, err = repo.Release(ctx, holding.ID, expiry.Add(-time.Minute))
		require.NoError(t, err)
		_, err = repo.Release(ctx, lapsed.ID, expiry.Add(time.Hour))
		require.NoError(t, err)
		assert.Empty(t, checkoutsOf(mark, holding.ID), "still holding")
		assert.Empty(t, checkoutsOf(mark, lapsed.ID), "already Expired")

		// @spec components/entity/checkout/release "Announcement cannot be recorded"
		failing := heldCheckout(t, db, repo, 1, start)
		_, err = rdb.NewCheckoutRepository(db, failingOutbox{}).Release(ctx, failing.ID, expiry.Add(time.Minute))
		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		after, err := repo.Get(ctx, failing.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, after.Status)
		assert.Equal(t, entity.OrderStatusPending, after.Order.Status)
	})

	t.Run("release", func(t *testing.T) {
		// @spec components/entity/checkout/release "Still holding"
		c := heldCheckout(t, db, repo, 2, start)
		changed, err := repo.Release(ctx, c.ID, expiry.Add(-time.Minute))
		require.NoError(t, err)
		assert.False(t, changed)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, got.Status)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)

		// @spec components/entity/checkout/release "Hold lapsed"
		changed, err = repo.Release(ctx, c.ID, expiry.Add(time.Minute))
		require.NoError(t, err)
		assert.True(t, changed)
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusExpired, got.Status)
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)

		// @spec components/entity/checkout/release "Committed just before the sweep"
		committed := heldCheckout(t, db, repo, 1, start)
		_, err = repo.Commit(ctx, committed.ID, expiry.Add(-time.Minute))
		require.NoError(t, err)
		changed, err = repo.Release(ctx, committed.ID, expiry.Add(time.Minute))
		require.NoError(t, err)
		assert.False(t, changed)
		got, err = repo.Get(ctx, committed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)

		// @spec components/entity/checkout/release "Paid checkout"
		paid := completedCheckout(t, db, repo, issuance, start)
		changed, err = repo.Release(ctx, paid.ID, expiry.Add(time.Hour))
		require.NoError(t, err)
		assert.False(t, changed)
		got, err = repo.Get(ctx, paid.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCompleted, got.Status)
		assert.Equal(t, entity.OrderStatusPaid, got.Order.Status)

		_, err = repo.Release(ctx, entity.CheckoutID(entity.NewID()), expiry)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("commit and sweep race", func(t *testing.T) {
		// A commit just before the expiry and the sweep just after it race;
		// whatever the interleaving, at most one of them changes the row, and
		// a committed row stays Committed with its Order Pending.
		for range 10 {
			c := heldCheckout(t, db, repo, 1, start)
			var wg sync.WaitGroup
			var outcome entity.CommitOutcome
			var released bool
			var commitErr, releaseErr error
			wg.Go(func() { outcome, commitErr = repo.Commit(ctx, c.ID, expiry.Add(-time.Second)) })
			wg.Go(func() { released, releaseErr = repo.Release(ctx, c.ID, expiry.Add(time.Second)) })
			wg.Wait()
			require.NoError(t, commitErr)
			require.NoError(t, releaseErr)

			got, err := repo.Get(ctx, c.ID)
			require.NoError(t, err)
			if outcome == entity.CommitOutcomeCommitted {
				assert.False(t, released)
				assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
				assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
			} else {
				assert.True(t, released)
				assert.Equal(t, entity.CheckoutStatusExpired, got.Status)
				assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
			}
		}
	})

	t.Run("revert commit", func(t *testing.T) {
		// @spec components/entity/checkout/revert-commit "Card closed after the commit"
		c := heldCheckout(t, db, repo, 2, start)
		committedAt := start.Add(10 * time.Minute)
		_, err := repo.Commit(ctx, c.ID, committedAt)
		require.NoError(t, err)
		sold, _ := stockCounts(t, db, c.Order.TicketStockID, committedAt)
		require.Equal(t, 2, sold)

		require.NoError(t, repo.RevertCommit(ctx, c.ID))

		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusReleased, got.Status)
		assert.Equal(t, committedAt, got.CommitTime.UTC())
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
		sold, _ = stockCounts(t, db, c.Order.TicketStockID, committedAt)
		assert.Equal(t, 0, sold)

		// @spec components/entity/checkout/revert-commit "Already charged"
		charged := heldCheckout(t, db, repo, 2, start)
		_, err = repo.Commit(ctx, charged.ID, committedAt)
		require.NoError(t, err)
		require.NoError(t, repo.RecordCapture(ctx, charged.ID, committedAt))
		assert.ErrorIs(t, repo.RevertCommit(ctx, charged.ID), apperr.ErrFailedPrecondition)
		got, err = repo.Get(ctx, charged.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)

		// A Checkout that is not Committed is left unchanged.
		held := heldCheckout(t, db, repo, 1, start)
		require.NoError(t, repo.RevertCommit(ctx, held.ID))
		got, err = repo.Get(ctx, held.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, got.Status)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)

		assert.ErrorIs(t, repo.RevertCommit(ctx, entity.CheckoutID(entity.NewID())), apperr.ErrNotFound)
	})

	t.Run("revert commit announces", func(t *testing.T) {
		committedAt := start.Add(10 * time.Minute)

		// @spec components/entity/checkout/revert-commit "Commit reverted"
		c := heldCheckout(t, db, repo, 2, start)
		_, err := repo.Commit(ctx, c.ID, committedAt)
		require.NoError(t, err)
		mark := outboxMark(t, db)
		require.NoError(t, repo.RevertCommit(ctx, c.ID))
		require.NoError(t, repo.RevertCommit(ctx, c.ID), "a second revert changes nothing")
		got := checkoutAnnouncements(t, db, mark, entity.SubjectCheckoutReleased, c.ID)
		require.Len(t, got, 1, "one checkout release is announced")
		assert.Equal(t, entityv1.CheckoutStatus_CHECKOUT_STATUS_RELEASED, got[0].GetStatus())

		// @spec components/entity/checkout/revert-commit "Announcement cannot be recorded"
		failing := heldCheckout(t, db, repo, 2, start)
		_, err = repo.Commit(ctx, failing.ID, committedAt)
		require.NoError(t, err)
		err = rdb.NewCheckoutRepository(db, failingOutbox{}).RevertCommit(ctx, failing.ID)
		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		after, err := repo.Get(ctx, failing.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, after.Status)
		assert.Equal(t, entity.OrderStatusPending, after.Order.Status)
	})

	t.Run("record capture", func(t *testing.T) {
		c := heldCheckout(t, db, repo, 2, start)
		_, err := repo.Commit(ctx, c.ID, start.Add(time.Minute))
		require.NoError(t, err)
		chargedAt := start.Add(14 * time.Minute)

		// @spec components/entity/checkout/record-capture "First charge"
		require.NoError(t, repo.RecordCapture(ctx, c.ID, chargedAt))
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, chargedAt, got.CaptureTime.UTC())

		// @spec components/entity/checkout/record-capture "Recorded twice"
		require.NoError(t, repo.RecordCapture(ctx, c.ID, chargedAt.Add(time.Hour)))
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, chargedAt, got.CaptureTime.UTC())

		held := heldCheckout(t, db, repo, 1, start)
		assert.ErrorIs(t, repo.RecordCapture(ctx, held.ID, chargedAt), apperr.ErrFailedPrecondition)
	})
}

// completedCheckout drives a checkout of a new fan for 2 tickets on a fresh
// first-come stock through commit, capture and issuance, returning it
// Completed.
func completedCheckout(t *testing.T, db *rdb.Database, repo *rdb.CheckoutRepository, issuance *rdb.IssuanceRepository, start time.Time) *entity.Checkout {
	t.Helper()
	f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 150, 10)
	return completedCheckoutOf(t, db, repo, issuance, f, nextFan(t, db), 2, start)
}

// completedCheckoutOf drives a checkout of fan for count tickets of the
// stock through card authorization, commit, capture and issuance, returning
// it Completed with its Paid Order.
func completedCheckoutOf(t *testing.T, db *rdb.Database, repo *rdb.CheckoutRepository, issuance *rdb.IssuanceRepository, f *stockFixture, fan entity.UserID, count int, start time.Time) *entity.Checkout {
	t.Helper()
	ctx := context.Background()
	c, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, count, nil, start)
	require.NoError(t, err)
	require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, newCardAuthorizationID(db)))
	_, err = repo.Commit(ctx, c.ID, start.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.RecordCapture(ctx, c.ID, start.Add(2*time.Minute)))
	order, tickets, settlement := checkoutOrder(t, db, c, start.Add(2*time.Minute))
	require.NoError(t, issuance.Issue(ctx, order, tickets, settlement))
	got, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	return got
}

// checkoutOrder returns the checkout's Pending Order, as stored, with the
// captured payment filled in and paid at now, together with its Tickets and
// its Held Settlement, as OrderUseCase builds them for
// IssuanceRepository.Issue.
func checkoutOrder(t *testing.T, db *rdb.Database, c *entity.Checkout, now time.Time) (*entity.Order, []*entity.Ticket, *entity.Settlement) {
	t.Helper()
	ctx := context.Background()
	order, err := rdb.NewOrderRepository(db).Get(ctx, c.OrderID)
	require.NoError(t, err)
	var eventID string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT event_id FROM ticket_stocks WHERE id = $1`, string(order.TicketStockID)).Scan(&eventID))
	order.Payment = entity.Payment{
		Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_" + string(c.ID), CardBrand: "visa", CardLast4: "4242",
	}
	order.PayTime = &now
	tickets := make([]*entity.Ticket, 0, order.TicketCount)
	for range order.TicketCount {
		tickets = append(tickets, &entity.Ticket{
			ID: entity.TicketID(entity.NewID()), OrderID: order.ID, UserID: order.UserID, EventID: eventID,
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued, IssueTime: now,
		})
	}
	settlement := entity.NewHeldSettlement(order, seedOrganizer(t, db), eventID, 800, now)
	return order, tickets, settlement
}

func TestCheckoutRepository_GetByCardAuthorizationID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewCheckoutRepository(db, testOutbox)
	ctx := context.Background()
	start := saleWindowStart.Add(time.Hour)

	t.Run("charged checkout", func(t *testing.T) {
		// @spec components/entity/checkout/get-by-card-authorization-id "Charged checkout"
		c := heldCheckout(t, db, repo, 1, start)
		ref := newCardAuthorizationID(db)
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, ref))

		got, err := repo.GetByCardAuthorizationID(ctx, ref)

		require.NoError(t, err)
		assert.Equal(t, c.ID, got.ID)
		assert.Equal(t, ref, got.Order.CardAuthorizationID)
	})

	t.Run("unknown card authorization", func(t *testing.T) {
		// @spec components/entity/checkout/get-by-card-authorization-id "Unknown card authorization"
		_, err := repo.GetByCardAuthorizationID(ctx, entity.NewID())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestCheckoutRepository_ListByUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewCheckoutRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("two checkouts", func(t *testing.T) {
		// @spec components/entity/checkout/list-by-user "Two checkouts"
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 100, 4)
		other := seedStockForEvent(t, db, seedPublishedEvent(t, db), entity.TicketSaleMethodFirstCome, 100, 4)
		userID := nextFan(t, db)
		now := time.Now()
		completed := insertCheckoutOrder(t, db, f, orderSeed{user: userID, status: entity.OrderStatusPaid, payAt: now.AddDate(0, -1, 0)},
			entity.CheckoutStatusCompleted, now.AddDate(0, -1, 0))
		held := insertCheckoutOrder(t, db, other, orderSeed{user: userID, status: entity.OrderStatusPending},
			entity.CheckoutStatusHeld, now.Add(14*time.Minute))
		insertCheckoutOrder(t, db, f, orderSeed{status: entity.OrderStatusPending}, entity.CheckoutStatusHeld, now.Add(14*time.Minute)) // another user

		got, err := repo.ListByUser(ctx, userID)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, held, got[0].ID)
		assert.Equal(t, entity.CheckoutStatusHeld, got[0].Status)
		assert.Equal(t, entity.OrderStatusPending, got[0].Order.Status)
		assert.Equal(t, other.ID, got[0].Order.TicketStockID)
		assert.Equal(t, completed, got[1].ID)
		assert.Equal(t, entity.CheckoutStatusCompleted, got[1].Status)
		assert.Equal(t, entity.OrderStatusPaid, got[1].Order.Status)
		for _, c := range got {
			assert.Equal(t, userID, c.Order.UserID)
		}
	})

	t.Run("never checked out", func(t *testing.T) {
		// @spec components/entity/checkout/list-by-user "Never checked out"
		got, err := repo.ListByUser(ctx, nextFan(t, db))

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}
