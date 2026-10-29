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
// same venue on a later date, with its publish state and per-account limit,
// and returns its id.
func seedEventInSeriesOf(t *testing.T, eventID string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx, `
		INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state, per_account_limit)
		SELECT $1, series_id, venue_id, local_event_date + $3::int, publish_state, per_account_limit FROM events WHERE id = $2
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

// newSaleFor returns an unsaved sale named ファンクラブ先行 over the Series of
// the first event, naming the events.
func newSaleFor(t *testing.T, method entity.TicketSaleMethod, start, end time.Time, eventIDs ...string) *entity.TicketSale {
	t.Helper()
	return entity.NewTicketSale(seriesOf(t, eventIDs[0]), "ファンクラブ先行", method, start, end, eventIDs...)
}

// createSale stores the sale with the repository.
func createSale(t *testing.T, sale *entity.TicketSale) *entity.TicketSale {
	t.Helper()
	created, err := rdb.NewTicketSaleRepository(testDB).Create(context.Background(), sale)
	require.NoError(t, err)
	return created
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

	assertNothingStored := func(t *testing.T, sale *entity.TicketSale) {
		t.Helper()
		_, err := repo.Get(ctx, sale.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Zero(t, queryCount(t, `SELECT COUNT(*) FROM ticket_sale_events WHERE ticket_sale_id = $1`, string(sale.ID)))
	}

	t.Run("sale stored", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "Sale stored"
		first := seedPublishedEvent(t)
		second := seedEventInSeriesOf(t, first)
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, first, second)

		created, err := repo.Create(ctx, sale)

		require.NoError(t, err)
		assert.ElementsMatch(t, []string{first, second}, created.EventIDs)
		assert.False(t, created.IsDrawnAt(end.Add(-time.Second), 0), "the sale is not drawn before its close")
		stored, err := repo.Get(ctx, sale.ID)
		require.NoError(t, err)
		assert.Equal(t, sale.SeriesID, stored.SeriesID)
		assert.Equal(t, "ファンクラブ先行", stored.Name)
		assert.Equal(t, entity.TicketSaleMethodLottery, stored.Method)
		assert.True(t, stored.SaleStartTime.Equal(saleWindowStart))
		assert.True(t, stored.SaleEndTime.Equal(end))
		assert.Nil(t, stored.DiscoveredTime)
		assert.ElementsMatch(t, []string{first, second}, stored.EventIDs)
	})

	t.Run("unknown event", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "Unknown event"
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, seedPublishedEvent(t))
		sale.EventIDs = append(sale.EventIDs, entity.NewID())

		_, err := repo.Create(ctx, sale)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assertNothingStored(t, sale)
	})

	t.Run("unknown series", func(t *testing.T) {
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, seedPublishedEvent(t))
		sale.SeriesID = entity.NewID()

		_, err := repo.Create(ctx, sale)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assertNothingStored(t, sale)
	})

	t.Run("same event twice", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "Same event twice"
		eventID := seedPublishedEvent(t)
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, eventID, eventID)

		_, err := repo.Create(ctx, sale)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assertNothingStored(t, sale)
	})

	t.Run("event of another series", func(t *testing.T) {
		// @spec components/entity/ticket-sale/create "Event of another Series"
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, seedPublishedEvent(t), seedPublishedEvent(t))

		_, err := repo.Create(ctx, sale)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assertNothingStored(t, sale)
	})

	t.Run("sale without an event", func(t *testing.T) {
		sale := newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, end, seedPublishedEvent(t))
		sale.EventIDs = nil

		_, err := repo.Create(ctx, sale)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assertNothingStored(t, sale)
	})
}

func TestTicketSaleRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()

	t.Run("sale covering several events", func(t *testing.T) {
		// @spec components/entity/ticket-sale/get "Sale covering several events"
		first := seedPublishedEvent(t)
		second := seedEventInSeriesOf(t, first)
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, saleWindowStart, saleWindowStart.Add(10*24*time.Hour), first, second))

		got, err := repo.Get(ctx, sale.ID)

		require.NoError(t, err)
		assert.Equal(t, sale.ID, got.ID)
		assert.ElementsMatch(t, []string{first, second}, got.EventIDs)
	})

	t.Run("unknown sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/get "Unknown sale"
		_, err := repo.Get(ctx, entity.TicketSaleID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// TestTicketSaleRepository_ListByEvent covers the sales naming an event. A
// sale is returned with its events; the event's stocks, with their counts,
// are read alongside it from TicketStockRepository.ListByEvent.
func TestTicketSaleRepository_ListByEvent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewTicketSaleRepository(testDB)
	stocks := rdb.NewTicketStockRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(17 * time.Hour)

	t.Run("presale and general sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "Presale and general sale"
		eventID := seedPublishedEvent(t)
		nov20 := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
		general := createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, nov20, nov20.Add(5*24*time.Hour), eventID))
		presale := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(7*24*time.Hour), eventID))

		got, err := repo.ListByEvent(ctx, eventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.TicketSaleID{presale.ID, general.ID}, saleIDs(got))
	})

	t.Run("sale covering several events", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "Sale covering several events"
		eventID := seedPublishedEvent(t)
		second, third := seedEventInSeriesOf(t, eventID), seedEventInSeriesOf(t, eventID)
		own := seedStockForEvent(t, eventID, entity.TicketSaleMethodLottery, 100, 4)
		seedStockForEvent(t, second, entity.TicketSaleMethodLottery, 100, 4)
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(7*24*time.Hour),
			eventID, second, third))

		got, err := repo.ListByEvent(ctx, eventID)

		require.NoError(t, err)
		require.Len(t, got, 2)
		var covering *entity.TicketSale
		for _, s := range got {
			if s.ID == sale.ID {
				require.Nil(t, covering, "the sale is returned once")
				covering = s
			}
		}
		require.NotNil(t, covering)
		assert.ElementsMatch(t, []string{eventID, second, third}, covering.EventIDs)
		eventStocks, err := stocks.ListByEvent(ctx, eventID, at)
		require.NoError(t, err)
		require.Len(t, eventStocks, 1, "this event's stocks only")
		assert.Equal(t, own.ID, eventStocks[0].ID)
	})

	t.Run("first-come sale with holds", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "First-come sale with holds"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		insertHolds(t, f, at.Add(entity.CheckoutHoldDuration), 3)

		got, err := repo.ListByEvent(ctx, f.EventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.TicketSaleID{f.SaleID}, saleIDs(got))
		eventStocks, err := stocks.ListByEvent(ctx, f.EventID, at)
		require.NoError(t, err)
		require.Len(t, eventStocks, 1)
		assert.Equal(t, 3, eventStocks[0].HeldCount)
	})

	t.Run("sold-out stock", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "Sold-out stock"
		f := seedStock(t, entity.TicketSaleMethodLottery, 150, 10)
		setStockSold(t, f, 150)

		got, err := repo.ListByEvent(ctx, f.EventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.TicketSaleID{f.SaleID}, saleIDs(got))
		eventStocks, err := stocks.ListByEvent(ctx, f.EventID, at)
		require.NoError(t, err)
		require.Len(t, eventStocks, 1)
		assert.Equal(t, 150, eventStocks[0].SoldCount)
	})

	t.Run("no sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-by-event "No sale"
		got, err := repo.ListByEvent(ctx, seedPublishedEvent(t))

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
	now := saleWindowStart.Add(7*24*time.Hour + 2*time.Minute)

	// lottery stores a lottery stock and a sale of it ending at end, with one
	// entry in each given state.
	lottery := func(t *testing.T, end time.Time, states ...entity.LotteryEntryState) entity.TicketSaleID {
		t.Helper()
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		_, err := testDB.Pool.Exec(ctx, `UPDATE ticket_sales SET sale_end_at = $2 WHERE id = $1`, string(f.SaleID), end)
		require.NoError(t, err)
		for _, s := range states {
			orderStatus := entity.OrderStatusPending
			if s == entity.LotteryEntryStateLost {
				orderStatus = entity.OrderStatusVoid
			}
			insertStockEntry(t, f, saleFan(t), orderStatus, s, 1)
		}
		return f.SaleID
	}
	won, lost, entered := entity.LotteryEntryStateWon, entity.LotteryEntryStateLost, entity.LotteryEntryStateEntered

	closed := lottery(t, now.Add(-2*time.Minute), entered, won)
	closedAMinuteAgo := lottery(t, now.Add(-time.Minute), entered)
	justClosed := lottery(t, now.Add(-30*time.Second), entered)
	open := lottery(t, now.Add(time.Second), entered)
	drawn := lottery(t, now.Add(-2*time.Minute), won, lost)
	empty := lottery(t, now.Add(-2*time.Minute))
	discoveredID, _, err := repo.UpsertDiscovered(ctx, &entity.DiscoveredTicketSale{
		SeriesID: seedSeriesOnly(t, "Discovered Tour"), Name: "オフィシャル先行", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: saleWindowStart, SaleEndTime: now.Add(-time.Hour),
	})
	require.NoError(t, err)

	got, err := repo.ListDueForDraw(ctx, now)
	require.NoError(t, err)
	ids := saleIDs(got)

	t.Run("closed and undrawn", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-due-for-draw "Closed and undrawn"
		assert.Contains(t, ids, closed, "ended 2 minutes before the instant")
		assert.Contains(t, ids, closedAMinuteAgo, "an end time 1 minute before the instant is due")
		for _, s := range got {
			assert.Len(t, s.EventIDs, 1, "each sale comes with its events")
		}
	})

	t.Run("just closed", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-due-for-draw "Just closed"
		assert.NotContains(t, ids, justClosed)
	})

	t.Run("still open", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-due-for-draw "Still open"
		assert.NotContains(t, ids, open)
	})

	t.Run("already drawn", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-due-for-draw "Already drawn"
		assert.NotContains(t, ids, drawn)
	})

	t.Run("closed without entries", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-due-for-draw "Closed without entries"
		assert.NotContains(t, ids, empty)
	})

	t.Run("discovered lottery has closed", func(t *testing.T) {
		// @spec components/entity/ticket-sale/list-due-for-draw "Discovered lottery has closed"
		assert.NotContains(t, ids, discoveredID)
	})

	t.Run("only the due sales", func(t *testing.T) {
		assert.ElementsMatch(t, []entity.TicketSaleID{closed, closedAMinuteAgo}, ids)
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
		first := seedPublishedEvent(t)
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, nov(1), nov(10), first, seedEventInSeriesOf(t, first)))

		got, err := repo.SetWindow(ctx, sale.ID, nov(2), nov(11))

		require.NoError(t, err)
		assert.True(t, got.SaleStartTime.Equal(nov(2)))
		assert.True(t, got.SaleEndTime.Equal(nov(11)))
		assert.ElementsMatch(t, sale.EventIDs, got.EventIDs)
		stored, err := repo.Get(ctx, sale.ID)
		require.NoError(t, err)
		assert.True(t, stored.SaleStartTime.Equal(nov(2)))
		assert.True(t, stored.SaleEndTime.Equal(nov(11)))
		assert.Equal(t, sale.Name, stored.Name)
		assert.Equal(t, sale.Method, stored.Method)
		assert.ElementsMatch(t, sale.EventIDs, stored.EventIDs)
	})

	t.Run("lottery window too long", func(t *testing.T) {
		// @spec components/entity/ticket-sale/set-window "Lottery window too long"
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, nov(1), nov(8), seedPublishedEvent(t)))

		_, err := repo.SetWindow(ctx, sale.ID, nov(1), nov(16))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		stored, err := repo.Get(ctx, sale.ID)
		require.NoError(t, err)
		assert.True(t, stored.SaleEndTime.Equal(nov(8)))
	})

	t.Run("end not after start", func(t *testing.T) {
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, nov(1), nov(8), seedPublishedEvent(t)))

		_, err := repo.SetWindow(ctx, sale.ID, nov(8), nov(8))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("unknown sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/set-window "Unknown sale"
		_, err := repo.SetWindow(ctx, entity.TicketSaleID(entity.NewID()), nov(2), nov(11))

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

		got, err := repo.SetVerificationRequirement(ctx, sale.ID, entity.VerificationRequirementJPKIOnly)

		require.NoError(t, err)
		assert.Equal(t, entity.VerificationRequirementJPKIOnly, got.VerificationRequirement)
		assert.True(t, got.SaleStartTime.Equal(sale.SaleStartTime))
		assert.True(t, got.SaleEndTime.Equal(sale.SaleEndTime))
		assert.Equal(t, sale.EventIDs, got.EventIDs)
		stored, err := repo.Get(ctx, sale.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.VerificationRequirementJPKIOnly, stored.VerificationRequirement)
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/ticket-sale/set-verification-requirement "Unknown id"
		_, err := repo.SetVerificationRequirement(ctx, entity.TicketSaleID(entity.NewID()), entity.VerificationRequirementJPKIOnly)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("no id", func(t *testing.T) {
		_, err := repo.SetVerificationRequirement(ctx, "", entity.VerificationRequirementJPKIOnly)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
