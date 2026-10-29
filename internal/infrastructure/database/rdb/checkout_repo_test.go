package rdb_test

import (
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
func newFan(t *testing.T, n string) entity.UserID {
	t.Helper()
	return entity.UserID(seedUser(t, "fan-"+n, "fan-"+n+"@example.com", "ext-fan-"+n))
}

// fanCounter makes fan names unique across the tests of the package.
var fanCounter struct {
	sync.Mutex
	n int
}

// nextFan seeds a new user with a unique name and returns its id.
func nextFan(t *testing.T) entity.UserID {
	t.Helper()
	fanCounter.Lock()
	fanCounter.n++
	n := fanCounter.n
	fanCounter.Unlock()
	return newFan(t, fmt.Sprintf("checkout-%d-%s", n, entity.NewID()[:8]))
}

// stockCounts returns the stock's sold and held counts at at, as
// TicketStockRepository.Get aggregates them.
func stockCounts(t *testing.T, id entity.TicketStockID, at time.Time) (sold, held int) {
	t.Helper()
	s, err := rdb.NewTicketStockRepository(testDB).Get(context.Background(), id, at)
	require.NoError(t, err)
	return s.SoldCount, s.HeldCount
}

// orderStatusOf returns the stored status of the order.
func orderStatusOf(t *testing.T, id entity.OrderID) entity.OrderStatus {
	t.Helper()
	var status int16
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		`SELECT status FROM orders WHERE id = $1`, string(id)).Scan(&status))
	return entity.OrderStatus(status)
}

func TestCheckoutRepository_GetOrCreateHeld(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)

	t.Run("first start", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "First start"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		setStockSold(t, f, 106)
		fan := nextFan(t)

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
		_, held := stockCounts(t, f.ID, at)
		assert.Equal(t, 2, held)
	})

	t.Run("double tap", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Double tap"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)

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
		_, held := stockCounts(t, f.ID, at)
		assert.Equal(t, 2, held)
	})

	t.Run("count changed", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Count changed"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)
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
		_, held := stockCounts(t, f.ID, at.Add(time.Minute))
		assert.Equal(t, 3, held)
	})

	t.Run("count raised when stock is short", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Count raised when stock is short"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 10, 4)
		setStockSold(t, f, 7)
		fan := nextFan(t)
		held, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)
		require.NoError(t, err)

		_, err = repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 4, nil, at)

		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
		still, err := repo.Get(ctx, held.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, still.Status)
		assert.Equal(t, entity.OrderStatusPending, still.Order.Status)
		_, heldCount := stockCounts(t, f.ID, at)
		assert.Equal(t, 2, heldCount, "the fan still holds 2 tickets")
	})

	t.Run("two fans, last ticket", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Two fans, last ticket"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 10, 4)
		setStockSold(t, f, 9)
		fans := []entity.UserID{nextFan(t), nextFan(t)}

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
		_, held := stockCounts(t, f.ID, at)
		assert.Equal(t, 1, held)
	})

	t.Run("over the per-account limit", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Over the per-account limit"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)
		seedIssuedTicketsOf(t, f, string(fan), 3)

		_, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 2, nil, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, held := stockCounts(t, f.ID, at)
		assert.Equal(t, 0, held, "nothing is held")

		// One more ticket still fits the limit of 4.
		_, err = repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 1, nil, at)
		assert.NoError(t, err)
	})

	t.Run("presale winner in the general sale", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Presale winner in the general sale"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		presale := seedSale(t, f.EventID, entity.TicketSaleMethodLottery,
			saleWindowStart.Add(-10*24*time.Hour), saleWindowStart.Add(-time.Hour))
		fan := nextFan(t)
		seedLotteryEntryInSale(t, f, presale, string(fan), entity.LotteryEntryStateWon, 4)

		_, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 1, nil, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, held := stockCounts(t, f.ID, at)
		assert.Equal(t, 0, held, "nothing is held")
	})

	t.Run("another seat type", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Another seat type"
		sSeats := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		aSeats := seedStockForEvent(t, sSeats.EventID, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)
		seedIssuedTicketsOf(t, sSeats, string(fan), 2)

		_, err := repo.GetOrCreateHeld(ctx, aSeats.ID, aSeats.SaleID, fan, 1, nil, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, held := stockCounts(t, aSeats.ID, at)
		assert.Equal(t, 0, held, "nothing is held")
	})

	t.Run("committed checkout counts until issued", func(t *testing.T) {
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)
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
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		c := completedCheckoutOf(t, repo, rdb.NewIssuanceRepository(testDB), f, nextFan(t), 2, at)

		_, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, c.Order.UserID, 2, nil, at)

		assert.NoError(t, err)
	})

	t.Run("a lapsed hold is expired when the fan starts again", func(t *testing.T) {
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)
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
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		_, err := repo.GetOrCreateHeld(ctx, entity.TicketStockID(entity.NewID()), f.SaleID, nextFan(t), 1, nil, at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("unknown user", func(t *testing.T) {
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		_, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, entity.UserID(entity.NewID()), 1, nil, at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// heldCheckout starts a checkout of a new fan for count tickets on a fresh
// first-come stock of 150 tickets with a per-account limit of 10.
func heldCheckout(t *testing.T, repo *rdb.CheckoutRepository, count int, at time.Time) *entity.Checkout {
	t.Helper()
	f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 10)
	c, err := repo.GetOrCreateHeld(context.Background(), f.ID, f.SaleID, nextFan(t), count, nil, at)
	require.NoError(t, err)
	return c
}

func TestCheckoutRepository_Lifecycle(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	issuance := rdb.NewIssuanceRepository(testDB)
	ctx := context.Background()
	start := saleWindowStart.Add(time.Hour)
	expiry := start.Add(15 * time.Minute)

	t.Run("get", func(t *testing.T) {
		// @spec components/entity/checkout/get "Unknown checkout"
		_, err := repo.Get(ctx, entity.CheckoutID(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)

		// @spec components/entity/checkout/get "Completed checkout"
		c := completedCheckout(t, repo, issuance, start)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCompleted, got.Status)
		assert.Equal(t, entity.OrderStatusPaid, got.Order.Status)
	})

	t.Run("set card authorization", func(t *testing.T) {
		c := heldCheckout(t, repo, 2, start)

		// @spec components/entity/checkout/set-card-authorization "First card authorization"
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_auth_1"))
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "pi_auth_1", got.Order.CardAuthorizationID)
		assert.Equal(t, c.Status, got.Status)
		assert.Equal(t, c.Order.TicketCount, got.Order.TicketCount)
		assert.Equal(t, c.Order.Amount, got.Order.Amount)
		assert.Equal(t, c.Order.Status, got.Order.Status)
		assert.True(t, c.HoldExpireTime.Equal(got.HoldExpireTime))

		// @spec components/entity/checkout/set-card-authorization "Same card authorization on retry"
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_auth_1"))
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "pi_auth_1", got.Order.CardAuthorizationID)

		// @spec components/entity/checkout/set-card-authorization "Another card authorization"
		assert.ErrorIs(t, repo.SetCardAuthorization(ctx, c.ID, "pi_other"), apperr.ErrFailedPrecondition)
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "pi_auth_1", got.Order.CardAuthorizationID, "nothing changes")

		// @spec components/entity/checkout/set-card-authorization "Expired checkout"
		lapsed := heldCheckout(t, repo, 1, start)
		_, err = repo.Release(ctx, lapsed.ID, expiry)
		require.NoError(t, err)
		assert.ErrorIs(t, repo.SetCardAuthorization(ctx, lapsed.ID, "pi_x"), apperr.ErrFailedPrecondition)
		got, err = repo.Get(ctx, lapsed.ID)
		require.NoError(t, err)
		assert.Empty(t, got.Order.CardAuthorizationID)

		assert.ErrorIs(t, repo.SetCardAuthorization(ctx, entity.CheckoutID(entity.NewID()), "pi_y"), apperr.ErrNotFound)
	})

	t.Run("commit", func(t *testing.T) {
		// @spec components/entity/checkout/commit "Within the hold"
		c := heldCheckout(t, repo, 2, start)
		outcome, err := repo.Commit(ctx, c.ID, start.Add(5*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
		assert.Equal(t, start.Add(5*time.Minute), got.CommitTime.UTC())
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
		sold, held := stockCounts(t, c.Order.TicketStockID, start.Add(5*time.Minute))
		assert.Equal(t, 2, sold)
		assert.Equal(t, 0, held)

		// @spec components/entity/checkout/commit "Repeated commit"
		outcome, err = repo.Commit(ctx, c.ID, start.Add(6*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		again, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, start.Add(5*time.Minute), again.CommitTime.UTC(), "the commit time is kept")
		sold, _ = stockCounts(t, c.Order.TicketStockID, start.Add(6*time.Minute))
		assert.Equal(t, 2, sold, "a repeated commit adds nothing")

		// @spec components/entity/checkout/commit "Hold lapsed"
		lapsed := heldCheckout(t, repo, 2, start)
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
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		setStockSold(t, f, 148)
		startedAt := time.Date(2026, 11, 2, 20, 5, 0, 0, time.UTC)
		c, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, nextFan(t), 2, nil, startedAt)
		require.NoError(t, err)
		committedAt := time.Date(2026, 11, 2, 20, 15, 0, 0, time.UTC)

		outcome, err := repo.Commit(ctx, c.ID, committedAt)

		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		stock, err := rdb.NewTicketStockRepository(testDB).Get(ctx, f.ID, committedAt)
		require.NoError(t, err)
		assert.Equal(t, 150, stock.SoldCount)
		assert.Equal(t, entity.TicketStockStateSoldOut, stock.State())
		assert.True(t, stock.IsResaleOpenAt(committedAt, committedAt.Add(24*time.Hour)))
	})

	t.Run("release", func(t *testing.T) {
		// @spec components/entity/checkout/release "Still holding"
		c := heldCheckout(t, repo, 2, start)
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
		committed := heldCheckout(t, repo, 1, start)
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
		paid := completedCheckout(t, repo, issuance, start)
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
			c := heldCheckout(t, repo, 1, start)
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
		c := heldCheckout(t, repo, 2, start)
		committedAt := start.Add(10 * time.Minute)
		_, err := repo.Commit(ctx, c.ID, committedAt)
		require.NoError(t, err)
		sold, _ := stockCounts(t, c.Order.TicketStockID, committedAt)
		require.Equal(t, 2, sold)

		require.NoError(t, repo.RevertCommit(ctx, c.ID))

		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusReleased, got.Status)
		assert.Equal(t, committedAt, got.CommitTime.UTC())
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
		sold, _ = stockCounts(t, c.Order.TicketStockID, committedAt)
		assert.Equal(t, 0, sold)

		// @spec components/entity/checkout/revert-commit "Already charged"
		charged := heldCheckout(t, repo, 2, start)
		_, err = repo.Commit(ctx, charged.ID, committedAt)
		require.NoError(t, err)
		require.NoError(t, repo.RecordCapture(ctx, charged.ID, committedAt))
		assert.ErrorIs(t, repo.RevertCommit(ctx, charged.ID), apperr.ErrFailedPrecondition)
		got, err = repo.Get(ctx, charged.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)

		// A Checkout that is not Committed is left unchanged.
		held := heldCheckout(t, repo, 1, start)
		require.NoError(t, repo.RevertCommit(ctx, held.ID))
		got, err = repo.Get(ctx, held.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, got.Status)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)

		assert.ErrorIs(t, repo.RevertCommit(ctx, entity.CheckoutID(entity.NewID())), apperr.ErrNotFound)
	})

	t.Run("record capture", func(t *testing.T) {
		c := heldCheckout(t, repo, 2, start)
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

		held := heldCheckout(t, repo, 1, start)
		assert.ErrorIs(t, repo.RecordCapture(ctx, held.ID, chargedAt), apperr.ErrFailedPrecondition)
	})

	t.Run("record card authorization release", func(t *testing.T) {
		// @spec components/entity/checkout/record-card-authorization-release "Card authorization given back"
		c := heldCheckout(t, repo, 1, start)
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_rel_"+string(c.ID)))
		_, err := repo.Release(ctx, c.ID, expiry)
		require.NoError(t, err)
		releasedAt := expiry.Add(time.Minute)

		require.NoError(t, repo.RecordCardAuthorizationRelease(ctx, c.ID, releasedAt))

		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, releasedAt, got.CardAuthorizationReleaseTime.UTC())
		require.NoError(t, repo.RecordCardAuthorizationRelease(ctx, c.ID, releasedAt.Add(time.Hour)), "already set: no change")
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, releasedAt, got.CardAuthorizationReleaseTime.UTC())

		held := heldCheckout(t, repo, 1, start)
		require.NoError(t, repo.SetCardAuthorization(ctx, held.ID, "pi_rel_"+string(held.ID)))
		assert.ErrorIs(t, repo.RecordCardAuthorizationRelease(ctx, held.ID, releasedAt), apperr.ErrFailedPrecondition, "a Held checkout")

		noCard := heldCheckout(t, repo, 1, start)
		_, err = repo.Release(ctx, noCard.ID, expiry)
		require.NoError(t, err)
		assert.ErrorIs(t, repo.RecordCardAuthorizationRelease(ctx, noCard.ID, releasedAt), apperr.ErrFailedPrecondition, "an Order without a card authorization")

		assert.ErrorIs(t, repo.RecordCardAuthorizationRelease(ctx, entity.CheckoutID(entity.NewID()), releasedAt), apperr.ErrNotFound)
	})
}

func TestCheckoutRepository_ListDue(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	t0 := saleWindowStart.Add(time.Hour)
	now := t0.Add(30 * time.Minute)

	// @spec components/entity/checkout/list-due "One of each"
	lapsed := heldCheckout(t, repo, 1, t0.Add(5*time.Minute)) // expired at t0+20m
	released := heldCheckout(t, repo, 1, t0)
	require.NoError(t, repo.SetCardAuthorization(ctx, released.ID, "pi_released_"+string(released.ID)))
	_, err := repo.Release(ctx, released.ID, t0.Add(16*time.Minute))
	require.NoError(t, err)
	stalled := heldCheckout(t, repo, 1, t0.Add(20*time.Minute))
	_, err = repo.Commit(ctx, stalled.ID, now.Add(-2*time.Minute))
	require.NoError(t, err)

	// @spec components/entity/checkout/list-due "Fresh commit"
	fresh := heldCheckout(t, repo, 1, t0.Add(20*time.Minute))
	_, err = repo.Commit(ctx, fresh.ID, now.Add(-30*time.Second))
	require.NoError(t, err)

	// @spec components/entity/checkout/list-due "Card authorization already given back"
	givenBack := heldCheckout(t, repo, 1, t0)
	require.NoError(t, repo.SetCardAuthorization(ctx, givenBack.ID, "pi_given_back_"+string(givenBack.ID)))
	_, err = repo.Release(ctx, givenBack.ID, t0.Add(16*time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.RecordCardAuthorizationRelease(ctx, givenBack.ID, t0.Add(17*time.Minute)))

	// An Expired checkout whose Order never had a card authorization has
	// nothing to give back.
	noCard := heldCheckout(t, repo, 1, t0)
	_, err = repo.Release(ctx, noCard.ID, t0.Add(16*time.Minute))
	require.NoError(t, err)

	due, err := repo.ListDue(ctx, now)

	require.NoError(t, err)
	ids := make([]entity.CheckoutID, 0, len(due))
	for _, r := range due {
		ids = append(ids, r.ID)
		assert.NotNil(t, r.Order, "every checkout is read with its order")
	}
	// Oldest hold first: released (t0+15m), lapsed (t0+20m), stalled (t0+35m).
	assert.Equal(t, []entity.CheckoutID{released.ID, lapsed.ID, stalled.ID}, ids)
}

func TestCheckoutRepository_Serialize(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	id := entity.CheckoutID(entity.NewID())

	// Two calls for the same checkout never overlap.
	var mu sync.Mutex
	inside, overlapped := 0, false
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			err := repo.Serialize(ctx, id, func(context.Context) error {
				mu.Lock()
				inside++
				if inside > 1 {
					overlapped = true
				}
				mu.Unlock()
				time.Sleep(50 * time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
			assert.NoError(t, err)
		})
	}
	wg.Wait()
	assert.False(t, overlapped)

	// fn's error is returned unchanged.
	want := apperr.ErrUnavailable
	assert.ErrorIs(t, repo.Serialize(ctx, id, func(context.Context) error { return want }), want)
}

// completedCheckout drives a checkout of a new fan for 2 tickets on a fresh
// first-come stock through commit, capture and issuance, returning it
// Completed.
func completedCheckout(t *testing.T, repo *rdb.CheckoutRepository, issuance *rdb.IssuanceRepository, start time.Time) *entity.Checkout {
	t.Helper()
	f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 10)
	return completedCheckoutOf(t, repo, issuance, f, nextFan(t), 2, start)
}

// completedCheckoutOf drives a checkout of fan for count tickets of the
// stock through card authorization, commit, capture and issuance, returning
// it Completed with its Paid Order.
func completedCheckoutOf(t *testing.T, repo *rdb.CheckoutRepository, issuance *rdb.IssuanceRepository, f *stockFixture, fan entity.UserID, count int, start time.Time) *entity.Checkout {
	t.Helper()
	ctx := context.Background()
	c, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, count, nil, start)
	require.NoError(t, err)
	require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_"+string(c.ID)))
	_, err = repo.Commit(ctx, c.ID, start.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.RecordCapture(ctx, c.ID, start.Add(2*time.Minute)))
	order, tickets, settlement := checkoutOrder(t, c, start.Add(2*time.Minute))
	require.NoError(t, issuance.Issue(ctx, order, tickets, settlement))
	got, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	return got
}

// checkoutOrder returns the checkout's Pending Order, as stored, with the
// captured payment filled in and paid at now, together with its Tickets and
// its Held Settlement, as OrderUseCase builds them for
// IssuanceRepository.Issue.
func checkoutOrder(t *testing.T, c *entity.Checkout, now time.Time) (*entity.Order, []*entity.Ticket, *entity.Settlement) {
	t.Helper()
	ctx := context.Background()
	order, err := rdb.NewOrderRepository(testDB).Get(ctx, c.OrderID)
	require.NoError(t, err)
	var eventID string
	require.NoError(t, testDB.Pool.QueryRow(ctx,
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
	settlement := entity.NewHeldSettlement(order, seedOrganizer(t), eventID, 800, now)
	return order, tickets, settlement
}

func TestCheckoutRepository_GetByCardAuthorizationID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	start := saleWindowStart.Add(time.Hour)

	t.Run("charged checkout", func(t *testing.T) {
		// @spec components/entity/checkout/get-by-card-authorization-id "Charged checkout"
		c := heldCheckout(t, repo, 1, start)
		ref := "pi_lookup_" + string(c.ID)
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, ref))

		got, err := repo.GetByCardAuthorizationID(ctx, ref)

		require.NoError(t, err)
		assert.Equal(t, c.ID, got.ID)
		assert.Equal(t, ref, got.Order.CardAuthorizationID)
	})

	t.Run("unknown card authorization", func(t *testing.T) {
		// @spec components/entity/checkout/get-by-card-authorization-id "Unknown card authorization"
		_, err := repo.GetByCardAuthorizationID(ctx, "pi_unknown_"+entity.NewID())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestCheckoutRepository_ListByUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()

	t.Run("two checkouts", func(t *testing.T) {
		// @spec components/entity/checkout/list-by-user "Two checkouts"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
		other := seedStockForEvent(t, seedPublishedEvent(t), entity.TicketSaleMethodFirstCome, 100, 4)
		userID := nextFan(t)
		now := time.Now()
		completed := insertCheckoutOrder(t, f, orderSeed{user: userID, status: entity.OrderStatusPaid, payAt: now.AddDate(0, -1, 0)},
			entity.CheckoutStatusCompleted, now.AddDate(0, -1, 0))
		held := insertCheckoutOrder(t, other, orderSeed{user: userID, status: entity.OrderStatusPending},
			entity.CheckoutStatusHeld, now.Add(14*time.Minute))
		insertCheckoutOrder(t, f, orderSeed{status: entity.OrderStatusPending}, entity.CheckoutStatusHeld, now.Add(14*time.Minute)) // another user

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
		got, err := repo.ListByUser(ctx, nextFan(t))

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}
