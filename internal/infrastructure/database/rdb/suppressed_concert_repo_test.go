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
	repo := rdb.NewConcertRepository(testDB)
	suppressedRepo := rdb.NewSuppressedConcertRepository(testDB)
	ctx := context.Background()
	localDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
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
		fan := seedFanUser(t)
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
		var seriesID string
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT series_id FROM events WHERE id = $1`, eventID).Scan(&seriesID))
		otherEventID := uuid.NewV7().String()
		exec(t, `INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2026-08-02')`,
			otherEventID, seriesID, venueID)
		discoveredID := uuid.NewV7().String()
		exec(t, `INSERT INTO ticket_sales (id, series_id, method, sale_start_at, sale_end_at, discovered_at) VALUES ($1, $2, 2, now(), now() + interval '1 day', now())`, discoveredID, seriesID)
		// A sale only for the deleted event, with a lottery entry, and a sale
		// that also offers the other event.
		onlyType := seedTicketTypeForEvent(t, eventID, entity.TicketSaleMethodLottery, 10, 4)
		entryID := entity.NewID()
		exec(t, `INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, state) VALUES ($1, $2, $3, 1, 5)`,
			entryID, string(onlyType.ID), seedFanUser(t))
		sharedType := seedTicketTypeForEvent(t, eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		otherTypeID := entity.NewID()
		exec(t, `INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit) VALUES ($1, $2, $3, 3000, 10, 4)`,
			otherTypeID, string(sharedType.TicketSaleID), otherEventID)

		require.NoError(t, repo.DeleteAndSuppress(ctx, eventID))

		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM series WHERE id = $1`, seriesID), "series")
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM ticket_sales WHERE id = $1`, discoveredID), "the series' discovered sale")
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM events WHERE id = $1`, otherEventID), "other event")
		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM ticket_types WHERE event_id = $1`, eventID), "the event's ticket types")
		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM lottery_entries WHERE id = $1`, entryID), "their lottery entries")
		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM ticket_sales WHERE id = $1`, string(onlyType.TicketSaleID)), "a sale left without a ticket type")
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM ticket_sales WHERE id = $1`, string(sharedType.TicketSaleID)), "a sale that still offers another event")
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM ticket_types WHERE id = $1`, otherTypeID), "the other event's ticket type")
	})

	t.Run("concert with issued tickets", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Concert with issued tickets"
		cleanDatabase(t)
		venueID := seedVenue(t, "Venue Three")
		artistID := seedArtist(t, "Artist Three", uuid.NewV7().String())
		eventID := seedEvent(t, venueID, artistID, "Sold", "2026-08-01")
		seedIssuedTickets(t, eventID, seedFanUser(t), 1)

		err := repo.DeleteAndSuppress(ctx, eventID)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM events WHERE id = $1`, eventID), "the event remains")
		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM suppressed_concerts`), "nothing is suppressed")
	})

	t.Run("concert with an abandoned checkout", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Concert with an abandoned checkout"
		cleanDatabase(t)
		venueID := seedVenue(t, "Venue Five")
		artistID := seedArtist(t, "Artist Five", uuid.NewV7().String())
		eventID := seedEvent(t, venueID, artistID, "Lapsed", "2026-08-01")
		tt := seedTicketTypeForEvent(t, eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		checkoutID := entity.NewID()
		exec(t, `INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, status, hold_expire_at, card_authorization_id, card_authorization_release_at)
			VALUES ($1, $2, $3, 1, 3000, 4, now() - interval '1 minute', 'pi_released', now())`, checkoutID, string(tt.ID), seedFanUser(t))

		require.NoError(t, repo.DeleteAndSuppress(ctx, eventID))

		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM events WHERE id = $1`, eventID), "event")
		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM ticket_types WHERE id = $1`, string(tt.ID)), "ticket type")
		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM checkouts WHERE id = $1`, checkoutID), "checkout")
		got, err := suppressedRepo.Exists(ctx, venueID, localDate, nil)
		require.NoError(t, err)
		assert.True(t, got, "the slot is suppressed")
	})

	t.Run("concert with a committed checkout is refused", func(t *testing.T) {
		cleanDatabase(t)
		venueID := seedVenue(t, "Venue Six")
		artistID := seedArtist(t, "Artist Six", uuid.NewV7().String())
		eventID := seedEvent(t, venueID, artistID, "Paying", "2026-08-01")
		tt := seedTicketTypeForEvent(t, eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		exec(t, `INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, status, hold_expire_at, card_authorization_id, commit_at)
			VALUES ($1, $2, $3, 1, 3000, 2, now() + interval '10 minutes', 'pi_test', now())`, entity.NewID(), string(tt.ID), seedFanUser(t))

		err := repo.DeleteAndSuppress(ctx, eventID)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM events WHERE id = $1`, eventID), "the event remains")
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM checkouts WHERE ticket_type_id = $1`, string(tt.ID)), "the checkout remains")
		assert.Equal(t, 0, count(t, `SELECT COUNT(*) FROM suppressed_concerts`), "nothing is suppressed")
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
		} {
			assert.False(t, rowExists(t, q.query, q.arg), what)
		}
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM suppressed_concerts`), "the slot is suppressed")
	})

	t.Run("cancelled concert with a failed order", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Cancelled concert with a failed order"
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		exec(t, `UPDATE events SET publish_state = 'CANCELLED' WHERE id = $1`, o.eventID)
		firstCome := seedTicketTypeForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		orderID := seedCheckoutOrder(t, firstCome.ID, entity.OrderStatusFailed, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), false)

		require.NoError(t, repo.DeleteAndSuppress(ctx, o.eventID))

		assert.False(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "event")
		assert.False(t, rowExists(t, `SELECT 1 FROM orders WHERE id = $1`, orderID), "failed order")
		assert.Equal(t, 1, count(t, `SELECT COUNT(*) FROM suppressed_concerts`), "the slot is suppressed")
	})

	t.Run("last event of a first-party series", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Last event of a first-party series"
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)

		require.NoError(t, repo.DeleteAndSuppress(ctx, o.eventID))

		assert.False(t, rowExists(t, `SELECT 1 FROM series WHERE id = $1`, o.seriesID), "series")
		assert.False(t, rowExists(t, `SELECT 1 FROM ticket_sales WHERE id = $1`, string(o.saleID)), "its ticket sale")
		assert.True(t, rowExists(t, `SELECT 1 FROM organizers WHERE id = $1`, o.organizerID), "organizer")
	})

	t.Run("other events of a first-party series", func(t *testing.T) {
		// @spec components/entity/concert/delete-and-suppress "Other events of a first-party series"
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		otherEventID := uuid.NewV7().String()
		exec(t, `INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state)
			SELECT $1, series_id, venue_id, '2026-11-21', 'PUBLISHED' FROM events WHERE id = $2`, otherEventID, o.eventID)

		require.NoError(t, repo.DeleteAndSuppress(ctx, o.eventID))

		assert.False(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "event")
		assert.True(t, rowExists(t, `SELECT 1 FROM series WHERE id = $1`, o.seriesID), "series")
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
				tt := seedTicketTypeForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
				exec(t, `INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, status, hold_expire_at)
					VALUES ($1, $2, $3, 1, 3000, 1, now() + interval '10 minutes')`, entity.NewID(), string(tt.ID), seedFanUser(t))
			},
		},
		{
			name: "concert with an expired checkout whose card authorization is open",
			setup: func(t *testing.T, o deletableOrganizer) {
				tt := seedTicketTypeForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
				exec(t, `INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, status, hold_expire_at, card_authorization_id)
					VALUES ($1, $2, $3, 1, 3000, 4, now() - interval '1 minute', 'pi_open')`, entity.NewID(), string(tt.ID), seedFanUser(t))
			},
		},
		{
			// @spec components/entity/concert/delete-and-suppress "Concert with an open lottery entry"
			name: "concert with an open lottery entry",
			setup: func(t *testing.T, o deletableOrganizer) {
				exec(t, `INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state)
					VALUES ($1, $2, $3, 1, 'pi_entered', 1)`, entity.NewID(), string(o.typeID), seedFanUser(t))
			},
		},
		{
			// @spec components/entity/concert/delete-and-suppress "Concert with a draft entry holding a card authorization"
			name: "concert with a draft entry holding a card authorization",
			setup: func(t *testing.T, o deletableOrganizer) {
				seedDraftEntryWithAuthorization(t, o.typeID, seedFanUser(t))
			},
		},
		{
			// @spec components/entity/concert/delete-and-suppress "Concert with a won entry awaiting its order"
			name: "concert with a won entry awaiting its order",
			setup: func(t *testing.T, o deletableOrganizer) {
				seedLotteryEntryWithState(t, o.typeID, seedFanUser(t), entity.LotteryEntryStateWon, 1)
			},
		},
		{
			name: "concert with a paid order",
			setup: func(t *testing.T, o deletableOrganizer) {
				seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, false)
			},
		},
	}
	for _, tc := range blocked {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			cleanDatabase(t)
			o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
			tc.setup(t, o)

			err := repo.DeleteAndSuppress(ctx, o.eventID)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			assert.True(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, o.eventID), "the event remains")
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
