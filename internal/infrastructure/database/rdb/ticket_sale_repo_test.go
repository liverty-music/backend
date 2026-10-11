package rdb_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// seriesEventDays counts the events seedEventInSeriesOf added, so each falls
// on its own date.
var seriesEventDays atomic.Int32

// seedEventInSeriesOf inserts another event of the Series of eventID, at the
// same venue on a later date, and returns its id.
func seedEventInSeriesOf(t *testing.T, eventID string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx, `
		INSERT INTO events (id, series_id, venue_id, local_event_date)
		SELECT $1, series_id, venue_id, local_event_date + $3::int FROM events WHERE id = $2
	`, id, eventID, seriesEventDays.Add(1))
	require.NoError(t, err)
	return id
}

// seriesOf returns the Series id of the event.
func seriesOf(t *testing.T, eventID string) string {
	t.Helper()
	var seriesID string
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `SELECT series_id FROM events WHERE id = $1`, eventID).Scan(&seriesID))
	return seriesID
}

// newSaleFor returns an unsaved sale over the Series of the first event, with
// one ticket type of 150 tickets at 3000 yen per event.
func newSaleFor(t *testing.T, method entity.TicketSaleMethod, start, end time.Time, eventIDs ...string) *entity.TicketSale {
	t.Helper()
	sale := entity.NewTicketSale(seriesOf(t, eventIDs[0]), "ファンクラブ先行", method, start, end)
	for _, eventID := range eventIDs {
		entity.NewTicketType(sale, eventID, 3000, 150, 4)
	}
	return sale
}

// createSale stores the sale with the repository.
func createSale(t *testing.T, sale *entity.TicketSale) *entity.TicketSale {
	t.Helper()
	created, err := rdb.NewTicketSaleRepository(testDB).Create(context.Background(), sale)
	require.NoError(t, err)
	return created
}

// insertHeldCheckout stores a Held checkout of count tickets on the ticket
// type whose hold lasts until holdExpire, for a new user.
func insertHeldCheckout(t *testing.T, ticketTypeID entity.TicketTypeID, count int, holdExpire time.Time) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, status, hold_expire_at)
		VALUES ($1, $2, $3, $4, $5, 1, $6)
	`, entity.NewID(), string(ticketTypeID), entity.NewID(), count, int64(count)*3000, holdExpire)
	require.NoError(t, err)
}

// ticketTypeIDs returns the ids of the ticket types, in order.
func ticketTypeIDs(types []*entity.TicketType) []entity.TicketTypeID {
	ids := make([]entity.TicketTypeID, len(types))
	for i, tt := range types {
		ids[i] = tt.ID
	}
	return ids
}

// saleIDs returns the ids of the sales, in order.
func saleIDs(sales []*entity.TicketSale) []entity.TicketSaleID {
	ids := make([]entity.TicketSaleID, len(sales))
	for i, s := range sales {
		ids[i] = s.ID
	}
	return ids
}

func TestTicketSaleRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	end := saleWindowStart.Add(7 * 24 * time.Hour)

	t.Run("sale stored", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "Sale stored"
		first := seedPublishedEvent(t)
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, first, seedEventInSeriesOf(t, first))

		created, err := repo.Create(ctx, sale)

		require.NoError(t, err)
		assert.Len(t, created.TicketTypes, 2)
		assert.False(t, created.IsDrawn())
		stored, err := repo.Get(ctx, sale.ID, saleWindowStart)
		require.NoError(t, err)
		assert.Equal(t, "ファンクラブ先行", stored.Name)
		assert.Equal(t, entity.TicketSaleMethodLottery, stored.Method)
		assert.True(t, stored.SaleStartTime.Equal(saleWindowStart))
		assert.True(t, stored.SaleEndTime.Equal(end))
		assert.Nil(t, stored.DrawTime)
		assert.ElementsMatch(t, ticketTypeIDs(sale.TicketTypes), ticketTypeIDs(stored.TicketTypes))
	})

	t.Run("first-come sale stored", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "First-come sale stored"
		sale := newSaleFor(t, entity.TicketSaleMethodFirstCome, saleWindowStart, end, seedPublishedEvent(t))

		created, err := repo.Create(ctx, sale)

		require.NoError(t, err)
		require.Len(t, created.TicketTypes, 1)
		assert.Equal(t, 0, created.TicketTypes[0].SoldCount)
		stored, err := repo.Get(ctx, sale.ID, saleWindowStart)
		require.NoError(t, err)
		require.Len(t, stored.TicketTypes, 1)
		assert.Equal(t, 150, stored.TicketTypes[0].Quantity)
		assert.Equal(t, 0, stored.TicketTypes[0].SoldCount)
	})

	t.Run("unknown event", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "Unknown event"
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, seedPublishedEvent(t))
		missing := entity.NewTicketType(sale, entity.NewID(), 3000, 150, 4)

		_, err := repo.Create(ctx, sale)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = repo.Get(ctx, sale.ID, saleWindowStart)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		_, _, err = repo.GetTicketType(ctx, sale.TicketTypes[0].ID, saleWindowStart)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		_, _, err = repo.GetTicketType(ctx, missing.ID, saleWindowStart)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("unknown series", func(t *testing.T) {
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, seedPublishedEvent(t))
		sale.SeriesID = entity.NewID()

		_, err := repo.Create(ctx, sale)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("same event twice", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "Same event twice"
		eventID := seedPublishedEvent(t)
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, eventID, eventID)

		_, err := repo.Create(ctx, sale)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		_, err = repo.Get(ctx, sale.ID, saleWindowStart)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("invalid ticket type", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "Invalid ticket type"
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, seedPublishedEvent(t))
		sale.TicketTypes[0].PerAccountLimit = 11

		_, err := repo.Create(ctx, sale)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		_, err = repo.Get(ctx, sale.ID, saleWindowStart)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketSaleRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(17 * time.Hour)

	t.Run("sale with holds", func(t *testing.T) {
		// @spec components/entity/ticket-sale/get "Sale with holds"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		insertHeldCheckout(t, tt.ID, 3, at.Add(entity.CheckoutHoldDuration))
		insertHeldCheckout(t, tt.ID, 2, at) // lapsed at the read time

		got, err := repo.Get(ctx, tt.TicketSaleID, at)

		require.NoError(t, err)
		require.Len(t, got.TicketTypes, 1)
		assert.Equal(t, 3, got.TicketTypes[0].HeldCount)
	})

	t.Run("sale covering several events", func(t *testing.T) {
		// @spec components/entity/ticket-sale/get "Sale covering several events"
		first := seedPublishedEvent(t)
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, saleWindowStart, saleWindowStart.Add(10*24*time.Hour),
			first, seedEventInSeriesOf(t, first)))

		got, err := repo.Get(ctx, sale.ID, at)

		require.NoError(t, err)
		assert.Equal(t, sale.ID, got.ID)
		assert.ElementsMatch(t, ticketTypeIDs(sale.TicketTypes), ticketTypeIDs(got.TicketTypes))
	})

	t.Run("unknown sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/get "Unknown sale"
		_, err := repo.Get(ctx, entity.TicketSaleID(entity.NewID()), at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketSaleRepository_ListByEvent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(17 * time.Hour)

	t.Run("presale and general sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "Presale and general sale"
		eventID := seedPublishedEvent(t)
		nov20 := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
		general := createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, nov20, nov20.Add(5*24*time.Hour), eventID))
		presale := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(7*24*time.Hour), eventID))

		got, err := repo.ListByEvent(ctx, eventID, at)

		require.NoError(t, err)
		assert.Equal(t, []entity.TicketSaleID{presale.ID, general.ID}, saleIDs(got))
	})

	t.Run("sale covering several events", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "Sale covering several events"
		eventID := seedPublishedEvent(t)
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(7*24*time.Hour),
			eventID, seedEventInSeriesOf(t, eventID), seedEventInSeriesOf(t, eventID)))

		got, err := repo.ListByEvent(ctx, eventID, at)

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, sale.ID, got[0].ID)
		assert.ElementsMatch(t, ticketTypeIDs(sale.TicketTypes), ticketTypeIDs(got[0].TicketTypes))
	})

	t.Run("first-come sale with holds", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "First-come sale with holds"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		insertHeldCheckout(t, tt.ID, 3, at.Add(entity.CheckoutHoldDuration))

		got, err := repo.ListByEvent(ctx, tt.EventID, at)

		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Len(t, got[0].TicketTypes, 1)
		assert.Equal(t, 3, got[0].TicketTypes[0].HeldCount)
	})

	t.Run("no sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "No sale"
		got, err := repo.ListByEvent(ctx, seedPublishedEvent(t), at)

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestTicketSaleRepository_ListDueForDraw(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	end := saleWindowStart.Add(7 * 24 * time.Hour)
	now := end.Add(time.Minute)

	closed := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, seedPublishedEvent(t)))
	closedAtNow := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, now, seedPublishedEvent(t)))
	open := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, now.Add(time.Second), seedPublishedEvent(t)))
	drawn := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, seedPublishedEvent(t)))
	_, err := testDB.Pool.Exec(ctx, `UPDATE ticket_sales SET draw_at = $2 WHERE id = $1`, string(drawn.ID), now)
	require.NoError(t, err)
	firstCome := createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, saleWindowStart, end, seedPublishedEvent(t)))

	got, err := repo.ListDueForDraw(ctx, now)
	require.NoError(t, err)
	ids := saleIDs(got)

	t.Run("closed and undrawn", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-due-for-draw "Closed and undrawn"
		assert.Contains(t, ids, closed.ID)
		assert.Contains(t, ids, closedAtNow.ID, "an end time at the instant is due")
		for _, s := range got {
			if s.ID == closed.ID {
				assert.ElementsMatch(t, ticketTypeIDs(closed.TicketTypes), ticketTypeIDs(s.TicketTypes))
			}
		}
	})

	t.Run("still open", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-due-for-draw "Still open"
		assert.NotContains(t, ids, open.ID)
	})

	t.Run("already drawn", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-due-for-draw "Already drawn"
		assert.NotContains(t, ids, drawn.ID)
	})

	t.Run("first-come sale", func(t *testing.T) {
		assert.NotContains(t, ids, firstCome.ID)
	})
}

func TestTicketSaleRepository_SetWindow(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	nov := func(day int) time.Time { return time.Date(2026, 11, day, 1, 0, 0, 0, time.UTC) }

	t.Run("window moved", func(t *testing.T) {
		// @spec components/entity/ticket-sale/set-window "Window moved"
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, nov(1), nov(10), seedPublishedEvent(t)))

		got, err := repo.SetWindow(ctx, sale.ID, nov(2), nov(11), nov(1))

		require.NoError(t, err)
		assert.True(t, got.SaleStartTime.Equal(nov(2)))
		assert.True(t, got.SaleEndTime.Equal(nov(11)))
		require.Len(t, got.TicketTypes, 1)
		want := *sale.TicketTypes[0]
		assert.Equal(t, &want, got.TicketTypes[0])
		stored, err := repo.Get(ctx, sale.ID, nov(1))
		require.NoError(t, err)
		assert.True(t, stored.SaleStartTime.Equal(nov(2)))
		assert.Equal(t, sale.Name, stored.Name)
	})

	t.Run("lottery window too long", func(t *testing.T) {
		// @spec components/entity/ticket-sale/set-window "Lottery window too long"
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, nov(1), nov(8), seedPublishedEvent(t)))

		_, err := repo.SetWindow(ctx, sale.ID, nov(1), nov(16), nov(1))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		stored, err := repo.Get(ctx, sale.ID, nov(1))
		require.NoError(t, err)
		assert.True(t, stored.SaleEndTime.Equal(nov(8)))
	})

	t.Run("unknown sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/set-window "Unknown sale"
		_, err := repo.SetWindow(ctx, entity.TicketSaleID(entity.NewID()), nov(2), nov(11), nov(1))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketSaleRepository_SetVerificationRequirement(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()

	t.Run("requirement changed", func(t *testing.T) {
		// @spec components/entity/ticket-sale/set-verification-requirement "Requirement changed"
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(7*24*time.Hour), seedPublishedEvent(t)))
		require.Equal(t, entity.VerificationRequirementNone, sale.VerificationRequirement)

		got, err := repo.SetVerificationRequirement(ctx, sale.ID, entity.VerificationRequirementJPKIOnly, saleWindowStart)

		require.NoError(t, err)
		assert.Equal(t, entity.VerificationRequirementJPKIOnly, got.VerificationRequirement)
		assert.True(t, got.SaleStartTime.Equal(sale.SaleStartTime))
		assert.True(t, got.SaleEndTime.Equal(sale.SaleEndTime))
		assert.ElementsMatch(t, ticketTypeIDs(sale.TicketTypes), ticketTypeIDs(got.TicketTypes))
		stored, err := repo.Get(ctx, sale.ID, saleWindowStart)
		require.NoError(t, err)
		assert.Equal(t, entity.VerificationRequirementJPKIOnly, stored.VerificationRequirement)
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/ticket-sale/set-verification-requirement "Unknown id"
		_, err := repo.SetVerificationRequirement(ctx, entity.TicketSaleID(entity.NewID()), entity.VerificationRequirementJPKIOnly, saleWindowStart)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("no id", func(t *testing.T) {
		_, err := repo.SetVerificationRequirement(ctx, "", entity.VerificationRequirementJPKIOnly, saleWindowStart)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestTicketSaleRepository_GetTicketType(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(17 * time.Hour)

	t.Run("existing ticket type", func(t *testing.T) {
		// @spec components/entity/ticket-type/get "Existing ticket type"
		first := seedPublishedEvent(t)
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(7*24*time.Hour),
			first, seedEventInSeriesOf(t, first)))
		want := sale.TicketTypes[1]

		gotSale, got, err := repo.GetTicketType(ctx, want.ID, at)

		require.NoError(t, err)
		assert.Equal(t, sale.ID, gotSale.ID)
		assert.Len(t, gotSale.TicketTypes, 2)
		assert.Equal(t, want, got)
		assert.Same(t, gotSale.TicketType(want.ID), got, "the ticket type is the sale's element")
	})

	t.Run("first-come ticket type with holds", func(t *testing.T) {
		// @spec components/entity/ticket-type/get "First-come ticket type with holds"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		insertHeldCheckout(t, tt.ID, 3, at.Add(entity.CheckoutHoldDuration))

		_, got, err := repo.GetTicketType(ctx, tt.ID, at)

		require.NoError(t, err)
		assert.Equal(t, 3, got.HeldCount)
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/ticket-type/get "Unknown id"
		_, _, err := repo.GetTicketType(ctx, entity.TicketTypeID(entity.NewID()), at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketSaleRepository_UpdateTicketType(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)

	// stored reads the ticket type back.
	stored := func(t *testing.T, id entity.TicketTypeID, at time.Time) *entity.TicketType {
		t.Helper()
		_, tt, err := repo.GetTicketType(ctx, id, at)
		require.NoError(t, err)
		return tt
	}

	t.Run("more tickets", func(t *testing.T) {
		// @spec components/entity/ticket-type/update "More tickets"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		setTicketTypeSold(t, tt.ID, 140)
		tt.Quantity = 180

		_, got, err := repo.UpdateTicketType(ctx, tt, at)

		require.NoError(t, err)
		assert.Equal(t, 180, got.Quantity)
		assert.Equal(t, 140, got.SoldCount)
		assert.Equal(t, 180, stored(t, tt.ID, at).Quantity)
	})

	t.Run("fewer than sold and held", func(t *testing.T) {
		// @spec components/entity/ticket-type/update "Fewer than sold and held"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 10)
		setTicketTypeSold(t, tt.ID, 100)
		insertHeldCheckout(t, tt.ID, 6, at.Add(entity.CheckoutHoldDuration))
		tt.Quantity = 105

		_, _, err := repo.UpdateTicketType(ctx, tt, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, 150, stored(t, tt.ID, at).Quantity)
	})

	t.Run("fewer than sold and held under a concurrent hold", func(t *testing.T) {
		// A checkout start and a shrink race for the last 5 tickets: either
		// the hold wins and the shrink fails, or the shrink wins and the hold
		// fails.
		checkouts := rdb.NewCheckoutRepository(testDB)
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 105, 10)
		setTicketTypeSold(t, tt.ID, 100)
		userID := entity.UserID(seedUser(t, "c", "c-type-upd@example.com", "ext-c-type-upd"))
		shrunk := *tt
		shrunk.Quantity = 100

		holdErr, updErr := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := checkouts.GetOrCreateHeld(ctx, tt.ID, userID, 5, at)
			holdErr <- err
		}()
		go func() {
			_, _, err := repo.UpdateTicketType(ctx, &shrunk, at)
			updErr <- err
		}()
		hErr, uErr := <-holdErr, <-updErr

		got := stored(t, tt.ID, at)
		if hErr == nil {
			assert.ErrorIs(t, uErr, apperr.ErrFailedPrecondition)
			assert.Equal(t, 105, got.Quantity)
			assert.Equal(t, 5, got.HeldCount)
		} else {
			assert.ErrorIs(t, hErr, apperr.ErrResourceExhausted)
			assert.NoError(t, uErr)
			assert.Equal(t, 100, got.Quantity)
		}
		assert.LessOrEqual(t, got.SoldCount+got.HeldCount, got.Quantity)
	})

	t.Run("price while tickets are held", func(t *testing.T) {
		// @spec components/entity/ticket-type/update "Price while tickets are held"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		insertHeldCheckout(t, tt.ID, 2, at.Add(entity.CheckoutHoldDuration))
		tt.Price = 3500

		_, _, err := repo.UpdateTicketType(ctx, tt, at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, int64(3000), stored(t, tt.ID, at).Price)
	})

	t.Run("price after every checkout lapsed", func(t *testing.T) {
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		insertHeldCheckout(t, tt.ID, 2, at.Add(entity.CheckoutHoldDuration))
		tt.Price = 3500

		_, got, err := repo.UpdateTicketType(ctx, tt, at.Add(entity.CheckoutHoldDuration))

		require.NoError(t, err)
		assert.Equal(t, int64(3500), got.Price)
	})

	t.Run("lottery ticket type resized", func(t *testing.T) {
		// @spec components/entity/ticket-type/update "Lottery ticket type resized"
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(7*24*time.Hour), seedPublishedEvent(t))
		sale.TicketTypes[0].Price, sale.TicketTypes[0].Quantity = 8000, 100
		tt := createSale(t, sale).TicketTypes[0]
		tt.Price, tt.Quantity = 9000, 120

		gotSale, got, err := repo.UpdateTicketType(ctx, tt, at)

		require.NoError(t, err)
		assert.Equal(t, sale.ID, gotSale.ID)
		assert.Equal(t, 120, got.Quantity)
		assert.Equal(t, int64(9000), got.Price)
		s := stored(t, tt.ID, at)
		assert.Equal(t, 120, s.Quantity)
		assert.Equal(t, int64(9000), s.Price)
	})

	t.Run("limit above the quantity", func(t *testing.T) {
		// @spec components/entity/ticket-type/update "Limit above the quantity"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 150, 4)
		tt.Quantity, tt.PerAccountLimit = 3, 4

		_, _, err := repo.UpdateTicketType(ctx, tt, at)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Equal(t, 150, stored(t, tt.ID, at).Quantity)
	})

	t.Run("unknown ticket type", func(t *testing.T) {
		// @spec components/entity/ticket-type/update "Unknown ticket type"
		missing := &entity.TicketType{ID: entity.TicketTypeID(entity.NewID()), Price: 3000, Quantity: 10, PerAccountLimit: 4}

		_, _, err := repo.UpdateTicketType(ctx, missing, at)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
