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

// saleWindowStart is the sale start of every seeded sale; tests read the sale
// inside its window.
var saleWindowStart = time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)

// seedTicketSale creates a FirstCome sale of quantity tickets at 3000 yen for
// a new published event.
func seedTicketSale(t *testing.T, quantity, limit int) *entity.TicketSale {
	t.Helper()
	repo := rdb.NewTicketSaleRepository(testDB)
	sale := entity.NewTicketSale(seedPublishedEvent(t), saleWindowStart, saleWindowStart.Add(20*24*time.Hour), 3000, quantity, limit, saleWindowStart)
	created, err := repo.Create(context.Background(), sale)
	require.NoError(t, err)
	return created
}

// setSold sets a sale's sold count directly, standing in for committed
// checkouts.
func setSold(t *testing.T, saleID entity.TicketSaleID, sold int) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(), `UPDATE ticket_sales SET sold_count = $2 WHERE id = $1`, string(saleID), sold)
	require.NoError(t, err)
}

func TestTicketSaleRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()

	t.Run("first sale for an event", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "First sale for an event"
		sale := seedTicketSale(t, 150, 0)

		got, err := repo.Get(ctx, sale.ID, saleWindowStart)
		require.NoError(t, err)
		assert.Equal(t, 0, got.SoldCount)
		assert.Equal(t, 150, got.Quantity)
		assert.Equal(t, 4, got.PerAccountLimit)
		assert.Equal(t, entity.TicketSaleMethodFirstCome, got.Method)
	})

	t.Run("second sale for the event", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "Second sale for the event"
		sale := seedTicketSale(t, 150, 0)
		second := entity.NewTicketSale(sale.EventID, saleWindowStart, saleWindowStart.Add(time.Hour), 3000, 10, 0, saleWindowStart)

		_, err := repo.Create(ctx, second)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		_, err = repo.Get(ctx, second.ID, saleWindowStart)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("invalid sale", func(t *testing.T) {
		bad := entity.NewTicketSale(seedPublishedEvent(t), saleWindowStart, saleWindowStart.Add(time.Hour), 3000, 10, 11, saleWindowStart)

		_, err := repo.Create(ctx, bad)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestTicketSaleRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	reservations := rdb.NewReservationRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)

	t.Run("sale with holds", func(t *testing.T) {
		// @spec components/entity/ticket-sale/get "Sale with holds"
		sale := seedTicketSale(t, 150, 0)
		_, err := reservations.GetOrCreateHeld(ctx, sale.ID, entity.UserID(seedUser(t, "a", "a-sale-get@example.com", "ext-a-sale-get")), 3, at, "")
		require.NoError(t, err)

		got, err := repo.Get(ctx, sale.ID, at)

		require.NoError(t, err)
		assert.Equal(t, 3, got.HeldCount)
		assert.True(t, got.HasReservations)

		// A hold that has lapsed by the read time no longer counts.
		later, err := repo.Get(ctx, sale.ID, at.Add(entity.ReservationHoldDuration))
		require.NoError(t, err)
		assert.Equal(t, 0, later.HeldCount)
	})

	t.Run("unknown sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/get "Unknown sale"
		_, err := repo.Get(ctx, entity.TicketSaleID(entity.NewID()), at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("event on sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/get-by-event "Event on sale"
		sale := seedTicketSale(t, 150, 0)

		got, err := repo.GetByEvent(ctx, sale.EventID, at)

		require.NoError(t, err)
		assert.Equal(t, sale.ID, got.ID)
		assert.Equal(t, 0, got.HeldCount)
	})

	t.Run("event without a sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/get-by-event "Event without a sale"
		_, err := repo.GetByEvent(ctx, seedPublishedEvent(t), at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketSaleRepository_Update(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	reservations := rdb.NewReservationRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)

	t.Run("more tickets", func(t *testing.T) {
		// @spec components/entity/ticket-sale/update "More tickets"
		sale := seedTicketSale(t, 150, 0)
		setSold(t, sale.ID, 140)
		sale.Quantity = 180

		got, err := repo.Update(ctx, sale, at)

		require.NoError(t, err)
		assert.Equal(t, 180, got.Quantity)
		stored, err := repo.Get(ctx, sale.ID, at)
		require.NoError(t, err)
		assert.Equal(t, 180, stored.Quantity)
	})

	t.Run("fewer than sold and held", func(t *testing.T) {
		// @spec components/entity/ticket-sale/update "Fewer than sold and held"
		sale := seedTicketSale(t, 150, 10)
		setSold(t, sale.ID, 100)
		_, err := reservations.GetOrCreateHeld(ctx, sale.ID, entity.UserID(seedUser(t, "b", "b-sale-upd@example.com", "ext-b-sale-upd")), 6, at, "")
		require.NoError(t, err)
		sale.Quantity = 105

		_, err = repo.Update(ctx, sale, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		stored, err := repo.Get(ctx, sale.ID, at)
		require.NoError(t, err)
		assert.Equal(t, 150, stored.Quantity)
	})

	t.Run("fewer than sold and held under a concurrent hold", func(t *testing.T) {
		// A hold and a shrink race for the last 5 tickets: either the hold
		// wins and the shrink fails, or the shrink wins and the hold fails.
		sale := seedTicketSale(t, 105, 10)
		setSold(t, sale.ID, 100)
		userID := entity.UserID(seedUser(t, "c", "c-sale-upd@example.com", "ext-c-sale-upd"))
		shrunk := *sale
		shrunk.Quantity = 100

		holdErr, updErr := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := reservations.GetOrCreateHeld(ctx, sale.ID, userID, 5, at, "")
			holdErr <- err
		}()
		go func() {
			_, err := repo.Update(ctx, &shrunk, at)
			updErr <- err
		}()
		hErr, uErr := <-holdErr, <-updErr

		stored, err := repo.Get(ctx, sale.ID, at)
		require.NoError(t, err)
		if hErr == nil {
			assert.ErrorIs(t, uErr, apperr.ErrFailedPrecondition)
			assert.Equal(t, 105, stored.Quantity)
			assert.Equal(t, 5, stored.HeldCount)
		} else {
			assert.ErrorIs(t, hErr, apperr.ErrResourceExhausted)
			assert.NoError(t, uErr)
			assert.Equal(t, 100, stored.Quantity)
		}
		assert.LessOrEqual(t, stored.SoldCount+stored.HeldCount, stored.Quantity)
	})

	t.Run("price after checkout started", func(t *testing.T) {
		// @spec components/entity/ticket-sale/update "Price after checkout started"
		sale := seedTicketSale(t, 150, 0)
		_, err := reservations.GetOrCreateHeld(ctx, sale.ID, entity.UserID(seedUser(t, "d", "d-sale-upd@example.com", "ext-d-sale-upd")), 1, at, "")
		require.NoError(t, err)
		sale.Price = 3500

		_, err = repo.Update(ctx, sale, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		stored, err := repo.Get(ctx, sale.ID, at)
		require.NoError(t, err)
		assert.Equal(t, int64(3000), stored.Price)
	})

	t.Run("unknown sale", func(t *testing.T) {
		missing := entity.NewTicketSale(seedPublishedEvent(t), saleWindowStart, saleWindowStart.Add(time.Hour), 3000, 10, 0, saleWindowStart)

		_, err := repo.Update(ctx, missing, at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
