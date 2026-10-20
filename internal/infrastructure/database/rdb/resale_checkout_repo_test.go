package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resaleOpenTime returns the ticket type's stored resale open time, nil when
// it has none.
func resaleOpenTime(t *testing.T, id entity.TicketTypeID) *time.Time {
	t.Helper()
	_, tt, err := rdb.NewTicketSaleRepository(testDB).GetTicketType(context.Background(), id, saleWindowStart)
	require.NoError(t, err)
	return tt.ResaleOpenTime
}

// heldListingIDs returns the ids of the Listed listings that point at the
// checkout, oldest first.
func heldListingIDs(t *testing.T, id entity.CheckoutID) []entity.ResaleListingID {
	t.Helper()
	rows, err := testDB.Pool.Query(context.Background(),
		`SELECT id FROM resale_listings WHERE checkout_id = $1 AND status = 1 ORDER BY created_at`, string(id))
	require.NoError(t, err)
	defer rows.Close()
	var ids []entity.ResaleListingID
	for rows.Next() {
		var lid string
		require.NoError(t, rows.Scan(&lid))
		ids = append(ids, entity.ResaleListingID(lid))
	}
	require.NoError(t, rows.Err())
	return ids
}

func TestCheckoutRepository_GetOrCreateHeld_Resale(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	day := saleWindowStart.Add(24 * time.Hour)

	t.Run("oldest listing first", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Oldest listing first"
		// @spec components/entity/checkout/get-or-create-held "Hold resale listings, oldest first"
		s := seedSoldOutType(t, 3)
		at10 := s.list(t, 0, day.Add(10*time.Hour))
		at11 := s.list(t, 1, day.Add(11*time.Hour))
		s.list(t, 2, day.Add(12*time.Hour))
		now := day.Add(13 * time.Hour)

		c, err := repo.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 2, now, &resaleDeadline)

		require.NoError(t, err)
		assert.True(t, c.Resale)
		assert.Equal(t, int64(6000), c.Amount)
		assert.Equal(t, now.Add(15*time.Minute), c.HoldExpireTime)
		assert.Equal(t, []entity.ResaleListingID{at10.ID, at11.ID}, heldListingIDs(t, c.ID))
		assert.Equal(t, 0, heldCount(t, s.tt.ID, now), "a resale hold is not a hold of stock")
	})

	t.Run("own listing skipped", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Own listing skipped"
		s := seedSoldOutType(t, 2)
		s.list(t, 0, day)
		// The second ticket belongs to another fan, who lists it after the
		// buyer's own listing.
		other := nextFan(t)
		_, err := testDB.Pool.Exec(ctx, `UPDATE tickets SET user_id = $2 WHERE id = $1`, string(s.tickets[1].ID), string(other))
		require.NoError(t, err)
		s.tickets[1].UserID = other
		next := s.list(t, 1, day.Add(time.Hour))

		c, err := repo.GetOrCreateHeld(ctx, s.tt.ID, s.seller, 1, day.Add(2*time.Hour), &resaleDeadline)

		require.NoError(t, err)
		assert.Equal(t, []entity.ResaleListingID{next.ID}, heldListingIDs(t, c.ID))
	})

	t.Run("not enough free listings", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Not enough free listings"
		s := seedSoldOutType(t, 2)
		s.list(t, 0, day)
		buyer := nextFan(t)

		_, err := repo.GetOrCreateHeld(ctx, s.tt.ID, buyer, 2, day.Add(time.Hour), &resaleDeadline)

		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
		var n int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM checkouts WHERE user_id = $1`, string(buyer)).Scan(&n))
		assert.Zero(t, n, "nothing is held")
	})

	t.Run("two fans, last listing", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Two fans, last listing"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, day)
		fans := []entity.UserID{nextFan(t), nextFan(t)}

		var wg sync.WaitGroup
		errs := make([]error, len(fans))
		for i, fan := range fans {
			wg.Go(func() {
				_, errs[i] = repo.GetOrCreateHeld(ctx, s.tt.ID, fan, 1, day.Add(time.Hour), &resaleDeadline)
			})
		}
		wg.Wait()

		var held, exhausted int
		for _, err := range errs {
			switch {
			case err == nil:
				held++
			case assert.ErrorIs(t, err, apperr.ErrResourceExhausted):
				exhausted++
			}
		}
		assert.Equal(t, 1, held)
		assert.Equal(t, 1, exhausted)
		got, err := rdb.NewResaleListingRepository(testDB).Get(ctx, listed.ID)
		require.NoError(t, err)
		assert.NotEmpty(t, got.CheckoutID)
	})

	t.Run("hold lapsed elsewhere", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Hold lapsed elsewhere"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, day)
		first, err := repo.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, day.Add(time.Hour), &resaleDeadline)
		require.NoError(t, err)
		released, err := repo.Release(ctx, first.ID, day.Add(time.Hour+15*time.Minute))
		require.NoError(t, err)
		require.True(t, released)

		c, err := repo.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, day.Add(2*time.Hour), &resaleDeadline)

		require.NoError(t, err)
		assert.Equal(t, []entity.ResaleListingID{listed.ID}, heldListingIDs(t, c.ID))
	})

	t.Run("resume the same resale checkout", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		s.list(t, 0, day)
		buyer := nextFan(t)
		first, err := repo.GetOrCreateHeld(ctx, s.tt.ID, buyer, 1, day.Add(time.Hour), &resaleDeadline)
		require.NoError(t, err)

		again, err := repo.GetOrCreateHeld(ctx, s.tt.ID, buyer, 1, day.Add(time.Hour+5*time.Minute), &resaleDeadline)

		require.NoError(t, err)
		assert.Equal(t, first.ID, again.ID)
		assert.True(t, first.HoldExpireTime.Equal(again.HoldExpireTime))
	})

	t.Run("replacing the fan's own hold frees its listings for the new one", func(t *testing.T) {
		s := seedSoldOutType(t, 2)
		s.list(t, 0, day)
		s.list(t, 1, day.Add(time.Minute))
		buyer := nextFan(t)
		first, err := repo.GetOrCreateHeld(ctx, s.tt.ID, buyer, 1, day.Add(time.Hour), &resaleDeadline)
		require.NoError(t, err)

		second, err := repo.GetOrCreateHeld(ctx, s.tt.ID, buyer, 2, day.Add(time.Hour+time.Minute), &resaleDeadline)

		require.NoError(t, err)
		assert.Len(t, heldListingIDs(t, second.ID), 2)
		replaced, err := repo.Get(ctx, first.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusReleased, replaced.Status)
	})

	t.Run("hold ends at the deadline", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		s.list(t, 0, day)
		now := resaleDeadline.Add(-10 * time.Minute)

		c, err := repo.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, now, &resaleDeadline)

		require.NoError(t, err)
		assert.True(t, c.HoldExpireTime.Equal(resaleDeadline))
	})
}

func TestCheckoutRepository_Resale_CommitAndRevert(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)

	t.Run("last tickets committed", func(t *testing.T) {
		// @spec components/entity/checkout/commit "Last tickets committed"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 10)
		setTicketTypeSold(t, tt.ID, 148)
		c, err := repo.GetOrCreateHeld(ctx, tt.ID, nextFan(t), 2, at, nil)
		require.NoError(t, err)

		_, err = repo.Commit(ctx, c.ID, at.Add(time.Minute))

		require.NoError(t, err)
		assert.Equal(t, 150, soldCount(t, tt.ID))
		got := resaleOpenTime(t, tt.ID)
		require.NotNil(t, got)
		assert.True(t, got.Equal(at.Add(time.Minute)))
	})

	t.Run("last ticket sold", func(t *testing.T) {
		// @spec components/entity/ticket-type "Last ticket sold"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 10)
		setTicketTypeSold(t, tt.ID, 149)
		c, err := repo.GetOrCreateHeld(ctx, tt.ID, nextFan(t), 1, at, nil)
		require.NoError(t, err)

		_, err = repo.Commit(ctx, c.ID, at.Add(time.Minute))

		require.NoError(t, err)
		got := resaleOpenTime(t, tt.ID)
		require.NotNil(t, got)
		assert.True(t, got.Equal(at.Add(time.Minute)))
	})

	t.Run("never sold out", func(t *testing.T) {
		// @spec components/entity/ticket-type "Never sold out"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 10)
		setTicketTypeSold(t, tt.ID, 148)
		c, err := repo.GetOrCreateHeld(ctx, tt.ID, nextFan(t), 1, at, nil)
		require.NoError(t, err)

		_, err = repo.Commit(ctx, c.ID, at.Add(time.Minute))

		require.NoError(t, err)
		assert.Equal(t, 149, soldCount(t, tt.ID))
		assert.Nil(t, resaleOpenTime(t, tt.ID))
	})

	t.Run("quantity raised after selling out", func(t *testing.T) {
		// @spec components/entity/ticket-type "Quantity raised after selling out"
		sales := rdb.NewTicketSaleRepository(testDB)
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 10)
		setTicketTypeSold(t, tt.ID, 149)
		c, err := repo.GetOrCreateHeld(ctx, tt.ID, nextFan(t), 1, at, nil)
		require.NoError(t, err)
		_, err = repo.Commit(ctx, c.ID, at.Add(time.Minute))
		require.NoError(t, err)

		raised := *tt
		raised.Quantity = 160
		sale, updated, err := sales.UpdateTicketType(ctx, &raised, at.Add(time.Hour))

		require.NoError(t, err)
		assert.Equal(t, entity.TicketTypeStateOnSale, updated.StateAt(sale, at.Add(time.Hour)))
		require.NotNil(t, updated.ResaleOpenTime)
		assert.True(t, updated.ResaleOpenTime.Equal(at.Add(time.Minute)))
	})

	t.Run("reverted commit keeps the resale open time", func(t *testing.T) {
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 10)
		setTicketTypeSold(t, tt.ID, 149)
		c, err := repo.GetOrCreateHeld(ctx, tt.ID, nextFan(t), 1, at, nil)
		require.NoError(t, err)
		_, err = repo.Commit(ctx, c.ID, at.Add(time.Minute))
		require.NoError(t, err)

		require.NoError(t, repo.RevertCommit(ctx, c.ID))

		assert.Equal(t, 149, soldCount(t, tt.ID))
		assert.NotNil(t, resaleOpenTime(t, tt.ID))
	})

	t.Run("resale checkout committed", func(t *testing.T) {
		// @spec components/entity/checkout/commit "Resale checkout committed"
		s := seedSoldOutType(t, 1)
		s.list(t, 0, at)
		c, err := repo.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, at, &resaleDeadline)
		require.NoError(t, err)

		outcome, err := repo.Commit(ctx, c.ID, at.Add(time.Minute))

		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		assert.Equal(t, 1, soldCount(t, s.tt.ID))
		assert.Len(t, heldListingIDs(t, c.ID), 1, "its listings stay held by it")
	})

	t.Run("resale card closed after the commit", func(t *testing.T) {
		// @spec components/entity/checkout/revert-commit "Resale card closed after the commit"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)
		c, err := repo.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, at, &resaleDeadline)
		require.NoError(t, err)
		_, err = repo.Commit(ctx, c.ID, at.Add(time.Minute))
		require.NoError(t, err)

		require.NoError(t, repo.RevertCommit(ctx, c.ID))

		reverted, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusReleased, reverted.Status)
		assert.Equal(t, 1, soldCount(t, s.tt.ID))
		l, err := rdb.NewResaleListingRepository(testDB).Get(ctx, listed.ID)
		require.NoError(t, err)
		assert.True(t, l.IsFreeAt(at.Add(2*time.Minute), resaleDeadline), "the listing is free again")
	})
}

func TestTicketSaleRepository_FreeResaleCount(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	sales := rdb.NewTicketSaleRepository(testDB)
	checkouts := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(24 * time.Hour)

	t.Run("sold-out ticket type with listings", func(t *testing.T) {
		// @spec components/entity/ticket-type/get "Sold-out ticket type with listings"
		s := seedSoldOutType(t, 3)
		for i := range 3 {
			s.list(t, i, at.Add(time.Duration(i)*time.Minute))
		}
		_, err := checkouts.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, at.Add(time.Hour), &resaleDeadline)
		require.NoError(t, err)

		_, got, err := sales.GetTicketType(ctx, s.tt.ID, at.Add(time.Hour))

		require.NoError(t, err)
		require.NotNil(t, got.ResaleOpenTime)
		assert.Equal(t, 2, got.FreeResaleCount)
		assert.Equal(t, 0, got.HeldCount)
	})

	t.Run("sold-out sale with free listings", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "Sold-out sale with free listings"
		s := seedSoldOutType(t, 2)
		s.list(t, 0, at)
		s.list(t, 1, at)

		got, err := sales.ListByEvent(ctx, s.tt.EventID, at.Add(time.Hour))

		require.NoError(t, err)
		require.Len(t, got, 1)
		tt := got[0].TicketType(s.tt.ID)
		require.NotNil(t, tt)
		require.NotNil(t, tt.ResaleOpenTime)
		assert.Equal(t, 2, tt.FreeResaleCount)
	})

	t.Run("a lapsed hold frees its listing", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		s.list(t, 0, at)
		_, err := checkouts.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, at, &resaleDeadline)
		require.NoError(t, err)

		_, during, err := sales.GetTicketType(ctx, s.tt.ID, at.Add(time.Minute))
		require.NoError(t, err)
		_, after, err := sales.GetTicketType(ctx, s.tt.ID, at.Add(15*time.Minute))
		require.NoError(t, err)

		assert.Equal(t, 0, during.FreeResaleCount)
		assert.Equal(t, 1, after.FreeResaleCount)
	})
}
