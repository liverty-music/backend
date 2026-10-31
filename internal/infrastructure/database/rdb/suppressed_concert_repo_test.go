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

func TestSuppressedConcertRepository(t *testing.T) {
	repo := rdb.NewSuppressedConcertRepository(testDB)
	ctx := context.Background()
	localDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 19, 0, 0, 0, time.UTC)

	newEntry := func(venueID string, startTime *time.Time) *entity.SuppressedConcert {
		return &entity.SuppressedConcert{
			ID:             uuid.NewV7().String(),
			VenueID:        venueID,
			LocalEventDate: localDate,
			StartTime:      startTime,
		}
	}

	t.Run("insert then exists matches the key; delete un-suppresses", func(t *testing.T) {
		cleanDatabase(t)
		venueID := uuid.NewV7().String()

		require.NoError(t, repo.Insert(ctx, newEntry(venueID, &start)))

		got, err := repo.Exists(ctx, venueID, localDate, &start)
		require.NoError(t, err)
		assert.True(t, got)

		// A different start time at the same venue/date is a distinct slot.
		other := start.Add(3 * time.Hour)
		got, err = repo.Exists(ctx, venueID, localDate, &other)
		require.NoError(t, err)
		assert.False(t, got)

		// Un-suppress removes the entry.
		require.NoError(t, repo.Delete(ctx, venueID, localDate, &start))
		got, err = repo.Exists(ctx, venueID, localDate, &start)
		require.NoError(t, err)
		assert.False(t, got)
	})

	t.Run("nil start collapses the slot (NULLS NOT DISTINCT)", func(t *testing.T) {
		cleanDatabase(t)
		venueID := uuid.NewV7().String()
		require.NoError(t, repo.Insert(ctx, newEntry(venueID, nil)))

		got, err := repo.Exists(ctx, venueID, localDate, nil)
		require.NoError(t, err)
		assert.True(t, got, "a nil start must match a suppressed unknown-start slot")
	})

	t.Run("insert is idempotent on the natural key", func(t *testing.T) {
		cleanDatabase(t)
		venueID := uuid.NewV7().String()
		require.NoError(t, repo.Insert(ctx, newEntry(venueID, &start)))
		require.NoError(t, repo.Insert(ctx, newEntry(venueID, &start))) // ON CONFLICT DO NOTHING

		var count int
		require.NoError(t, testDB.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM suppressed_concerts WHERE venue_id = $1`, venueID).Scan(&count))
		assert.Equal(t, 1, count)
	})
}

func TestConcertRepository_DeleteAndSuppress(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewConcertRepository(testDB, testOutbox)
	suppressedRepo := rdb.NewSuppressedConcertRepository(testDB)
	ctx := context.Background()
	localDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	released := saleWindowStart.Add(time.Hour)
	exec := func(t *testing.T, query string, args ...any) {
		t.Helper()
		_, err := testDB.Pool.Exec(ctx, query, args...)
		require.NoError(t, err)
	}
	count := func(t *testing.T, query string, args ...any) int {
		t.Helper()
		var n int
		require.NoError(t, testDB.Pool.QueryRow(ctx, query, args...).Scan(&n))
		return n
	}

	t.Run("concert with fan journeys", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Concert with fan journeys"
		cleanDatabase(t)
		venueID := seedVenue(t, "Venue One")
		artistID := seedArtist(t, "Artist One", uuid.NewV7().String())
		eventID := seedEvent(t, venueID, artistID, "Show", "2026-08-01")
		fan := saleFan(t)
		exec(t, `INSERT INTO ticket_journeys (user_id, event_id, status) VALUES ($1, $2, 1)`, fan, eventID)

		require.NoError(t, repo.DeleteAndSuppress(ctx, eventID))

		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM events WHERE id = $1`, eventID), "event")
		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM ticket_journeys WHERE event_id = $1`, eventID), "ticket journeys")
		got, err := suppressedRepo.Exists(ctx, venueID, localDate, nil)
		require.NoError(t, err)
		assert.True(t, got, "the slot is suppressed")
	})

	t.Run("series stays", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Series stays"
		cleanDatabase(t)
		venueID := seedVenue(t, "Venue Two")
		artistID := seedArtist(t, "Artist Two", uuid.NewV7().String())
		eventID := seedEvent(t, venueID, artistID, "Tour", "2026-08-01")
		seriesID := seriesOf(t, eventID)
		otherEventID := uuid.NewV7().String()
		exec(t, `INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2026-08-02')`,
			otherEventID, seriesID, venueID)
		discoveredID := uuid.NewV7().String()
		exec(t, `INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at, discovered_at)
			VALUES ($1, $2, 'オフィシャル先行', 2, now(), now() + interval '1 day', now())`, discoveredID, seriesID)

		require.NoError(t, repo.DeleteAndSuppress(ctx, eventID))

		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM series WHERE id = $1`, seriesID), "series")
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM ticket_sales WHERE id = $1`, discoveredID), "the series' discovered sale")
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM events WHERE id = $1`, otherEventID), "other event")
	})

	t.Run("an Organizer's sale left naming no event is removed", func(t *testing.T) {
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		otherEventID := seedEventInSeriesOf(t, o.eventID)
		// The fixture's lottery names only the deleted event; it has a Lost
		// entry. Another sale names both events.
		entryID, orderID := insertStockEntry(t, o.stock, saleFan(t), entity.OrderStatusVoid, entity.LotteryEntryStateLost, 1)
		shared := createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, saleWindowStart, saleWindowStart.Add(24*time.Hour), o.eventID, otherEventID))
		otherStock := seedStockForEvent(t, otherEventID, entity.TicketSaleMethodFirstCome, 10, 4)

		require.NoError(t, repo.DeleteAndSuppress(ctx, o.eventID))

		assert.False(t, rowExists(t, `SELECT 1 FROM ticket_stocks WHERE id = $1`, string(o.stock.ID)), "the event's ticket stock")
		assert.False(t, rowExists(t, `SELECT 1 FROM lottery_entries WHERE id = $1`, entryID), "its lost entry")
		assert.False(t, rowExists(t, `SELECT 1 FROM orders WHERE id = $1`, orderID), "its void order")
		assert.False(t, rowExists(t, `SELECT 1 FROM ticket_sales WHERE id = $1`, string(o.saleID)), "a sale left naming no event")
		assert.True(t, rowExists(t, `SELECT 1 FROM ticket_sales WHERE id = $1`, string(shared.ID)), "a sale that still names another event")
		assert.Equal(t, []string{otherEventID}, mustGetSale(t, shared.ID).EventIDs)
		assert.True(t, rowExists(t, `SELECT 1 FROM ticket_stocks WHERE id = $1`, string(otherStock.ID)), "the other event's ticket stock")
	})

	t.Run("concert with issued tickets", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Concert with issued tickets"
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		seedIssuedTicketsOf(t, o.stock, saleFan(t), 1)

		err := repo.DeleteAndSuppress(ctx, o.eventID)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM events WHERE id = $1`, o.eventID), "the event remains")
		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM suppressed_concerts`), "nothing is suppressed")
	})

	t.Run("concert with an abandoned checkout", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Concert with an abandoned checkout"
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		checkoutID, orderID := insertStockCheckout(t, f, saleFan(t), entity.OrderStatusVoid, entity.CheckoutStatusExpired, 1, saleWindowStart, true, &released)

		require.NoError(t, repo.DeleteAndSuppress(ctx, o.eventID))

		assert.False(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "event")
		assert.False(t, rowExists(t, `SELECT 1 FROM ticket_stocks WHERE id = $1`, string(f.ID)), "ticket stock")
		assert.False(t, rowExists(t, `SELECT 1 FROM orders WHERE id = $1`, orderID), "void order")
		assert.False(t, rowExists(t, `SELECT 1 FROM checkouts WHERE id = $1`, checkoutID), "checkout")
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM suppressed_concerts`), "the slot is suppressed")
	})

	t.Run("cancelled concert with refunded purchases", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Cancelled concert with refunded purchases"
		cleanDatabase(t)
		e := seedRemovableEvent(t, entity.OrganizerStatusActive)

		require.NoError(t, repo.DeleteAndSuppress(ctx, e.eventID))

		assert.False(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, e.eventID), "event")
		assertPurchaseRecords(t, e.lottery, false)
		for what, q := range map[string]struct {
			query string
			arg   any
		}{
			"checkout order":  {`SELECT 1 FROM orders WHERE id = $1`, e.checkoutOrderID},
			"checkout ticket": {`SELECT 1 FROM tickets WHERE id = $1`, e.checkoutTicket},
			"checkout":        {`SELECT 1 FROM checkouts WHERE id = $1`, e.checkoutID},
			"resale listing":  {`SELECT 1 FROM resale_listings WHERE id = $1`, e.resaleListingID},
			"scanner":         {`SELECT 1 FROM scanners WHERE id = $1`, string(e.scannerID)},
			"admission":       {`SELECT 1 FROM admissions WHERE ticket_id = $1`, e.lottery.ticketID},
			"rejected scan":   {`SELECT 1 FROM rejected_scans WHERE event_id = $1`, e.eventID},
			"ticket stock":    {`SELECT 1 FROM ticket_stocks WHERE event_id = $1`, e.eventID},
		} {
			assert.False(t, rowExists(t, q.query, q.arg), what)
		}
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM suppressed_concerts`), "the slot is suppressed")
	})

	t.Run("last event of a first-party series", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Last event of a first-party series"
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)

		require.NoError(t, repo.DeleteAndSuppress(ctx, o.eventID))

		assert.False(t, rowExists(t, `SELECT 1 FROM series WHERE id = $1`, o.seriesID), "series")
		assert.False(t, rowExists(t, `SELECT 1 FROM seat_types WHERE series_id = $1`, o.seriesID), "its seat types")
		assert.False(t, rowExists(t, `SELECT 1 FROM ticket_sales WHERE id = $1`, string(o.saleID)), "its ticket sale")
		assert.True(t, rowExists(t, `SELECT 1 FROM organizers WHERE id = $1`, o.organizerID), "organizer")
	})

	t.Run("other events of a first-party series", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Other events of a first-party series"
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		otherEventID := uuid.NewV7().String()
		exec(t, `INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state, per_account_limit)
			SELECT $1, series_id, venue_id, '2026-11-21', 'PUBLISHED', 4 FROM events WHERE id = $2`, otherEventID, o.eventID)

		require.NoError(t, repo.DeleteAndSuppress(ctx, o.eventID))

		assert.False(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "event")
		assert.True(t, rowExists(t, `SELECT 1 FROM series WHERE id = $1`, o.seriesID), "series")
		assert.True(t, rowExists(t, `SELECT 1 FROM seat_types WHERE id = $1`, string(o.stock.SeatType.ID)), "its seat type")
		assert.True(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, otherEventID), "other event")
	})

	blocked := []struct {
		name  string
		setup func(t *testing.T, o deletableOrganizer)
	}{
		{
			// @spec components/entity/concert/delete-and-suppress "Concert with a held settlement"
			name: "concert with a held settlement",
			setup: func(t *testing.T, o deletableOrganizer) {
				seedPurchase(t, o, entity.OrderStatusRefunded, entity.SettlementStatusHeld, false)
			},
		},
		{
			// @spec components/entity/concert/delete-and-suppress "Concert with a checkout in progress"
			name: "concert with a checkout in progress",
			setup: func(t *testing.T, o deletableOrganizer) {
				f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
				insertStockCheckout(t, f, saleFan(t), entity.OrderStatusPending, entity.CheckoutStatusHeld, 1, time.Now().Add(10*time.Minute), false, nil)
			},
		},
		{
			name: "concert with a committed checkout",
			setup: func(t *testing.T, o deletableOrganizer) {
				f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
				insertStockCheckout(t, f, saleFan(t), entity.OrderStatusPending, entity.CheckoutStatusCommitted, 1, time.Now().Add(10*time.Minute), true, nil)
			},
		},
		{
			name: "concert with an expired checkout whose card authorization is open",
			setup: func(t *testing.T, o deletableOrganizer) {
				f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
				insertStockCheckout(t, f, saleFan(t), entity.OrderStatusVoid, entity.CheckoutStatusExpired, 1, time.Now().Add(-time.Minute), true, nil)
			},
		},
		{
			// @spec components/entity/concert/delete-and-suppress "Concert with an open lottery entry"
			name: "concert with an open lottery entry",
			setup: func(t *testing.T, o deletableOrganizer) {
				insertStockEntry(t, o.stock, saleFan(t), entity.OrderStatusPending, entity.LotteryEntryStateEntered, 1)
			},
		},
		{
			// @spec components/entity/concert/delete-and-suppress "Concert with a won entry awaiting its order"
			name: "concert with a won entry awaiting its order",
			setup: func(t *testing.T, o deletableOrganizer) {
				insertStockEntry(t, o.stock, saleFan(t), entity.OrderStatusPending, entity.LotteryEntryStateWon, 1)
			},
		},
		{
			// @spec stories/remove-a-cancelled-concert "Delete before refunds"
			name: "cancelled concert with a paid order",
			setup: func(t *testing.T, o deletableOrganizer) {
				exec(t, `UPDATE events SET publish_state = 'CANCELLED' WHERE id = $1`, o.eventID)
				seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, false)
			},
		},
	}
	// purchaseRecords counts the event's purchase records, table by table.
	purchaseRecords := func(t *testing.T, eventID string) map[string]int {
		t.Helper()
		const orders = `SELECT o.id FROM orders o JOIN ticket_stocks s ON s.id = o.ticket_stock_id WHERE s.event_id = $1`
		return map[string]int{
			"ticket stocks":   count(t, `SELECT COUNT(*) FROM ticket_stocks WHERE event_id = $1`, eventID),
			"lottery entries": count(t, `SELECT COUNT(*) FROM lottery_entries WHERE order_id IN (`+orders+`)`, eventID),
			"checkouts":       count(t, `SELECT COUNT(*) FROM checkouts WHERE order_id IN (`+orders+`)`, eventID),
			"orders":          count(t, `SELECT COUNT(*) FROM (`+orders+`) x`, eventID),
			"tickets":         count(t, `SELECT COUNT(*) FROM tickets WHERE order_id IN (`+orders+`)`, eventID),
			"settlements":     count(t, `SELECT COUNT(*) FROM settlements WHERE event_id = $1`, eventID),
		}
	}
	for _, tc := range blocked {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			cleanDatabase(t)
			o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
			tc.setup(t, o)
			before := purchaseRecords(t, o.eventID)

			err := repo.DeleteAndSuppress(ctx, o.eventID)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			assert.True(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "the event remains")
			assert.Equal(t, before, purchaseRecords(t, o.eventID), "every purchase record remains")
			assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM suppressed_concerts`), "nothing is suppressed")
		})
	}

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Unknown id"
		cleanDatabase(t)
		require.NoError(t, repo.DeleteAndSuppress(ctx, uuid.NewV7().String()))

		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM suppressed_concerts`))
	})

	t.Run("slot already suppressed", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Slot already suppressed"
		cleanDatabase(t)
		venueID := seedVenue(t, "Venue Four")
		artistID := seedArtist(t, "Artist Four", uuid.NewV7().String())
		eventID := seedEvent(t, venueID, artistID, "Again", "2026-08-01")
		require.NoError(t, suppressedRepo.Insert(ctx, &entity.SuppressedConcert{
			ID: uuid.NewV7().String(), VenueID: venueID, LocalEventDate: localDate,
		}))

		require.NoError(t, repo.DeleteAndSuppress(ctx, eventID))

		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM suppressed_concerts WHERE venue_id = $1`, venueID))
	})
}

// mustGetSale reads the sale with the repository.
func mustGetSale(t *testing.T, id entity.TicketSaleID) *entity.TicketSale {
	t.Helper()
	sale, err := rdb.NewTicketSaleRepository(testDB).Get(context.Background(), id)
	require.NoError(t, err)
	return sale
}
