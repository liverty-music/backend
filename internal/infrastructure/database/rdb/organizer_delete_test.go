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

// deletableOrganizer holds the ids of an Organizer seeded with one published
// Series, one Event (with a performer, a lottery TicketStock of a SeatType
// sold by a TicketSale, and a Scanner), a cover SeriesImage, a second
// SeriesImage no Series uses, and one associated Artist that a fan follows.
type deletableOrganizer struct {
	organizerID string
	seriesID    string
	eventID     string
	saleID      entity.TicketSaleID
	stock       *stockFixture
	coverID     string
	unusedID    string
	artistID    string
	followerID  string
	scannerID   entity.ScannerID
}

// seedDeletableOrganizer seeds a deletableOrganizer in the given status.
func seedDeletableOrganizer(t *testing.T, status entity.OrganizerStatus) deletableOrganizer {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := testDB.Pool.Exec(ctx, query, args...)
		require.NoError(t, err)
	}

	o := deletableOrganizer{
		organizerID: uuid.NewV7().String(),
		seriesID:    uuid.NewV7().String(),
		eventID:     uuid.NewV7().String(),
		coverID:     uuid.NewV7().String(),
		unusedID:    uuid.NewV7().String(),
	}
	exec(`INSERT INTO organizers (id, name, short_name, owner_email, status, provider_organizer_id) VALUES ($1, 'Delete Organizer', $4, 'op@example.com', $2, $3)`,
		o.organizerID, int16(status), "org-"+o.organizerID, testShortName(o.organizerID))
	o.artistID = seedArtist(t, "delete-artist", uuid.NewV7().String())
	exec(`INSERT INTO organizer_artists (organizer_id, artist_id) VALUES ($1, $2)`, o.organizerID, o.artistID)
	o.followerID = seedUser(t, "delete-follower", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
	exec(`INSERT INTO followed_artists (user_id, artist_id) VALUES ($1, $2)`, o.followerID, o.artistID)

	venueID := seedVenue(t, "delete-venue-"+uuid.NewV7().String())
	exec(`INSERT INTO series (id, title, type, organizer_id, visibility) VALUES ($1, 'Delete Tour', 'SINGLE', $2, 'PUBLIC')`,
		o.seriesID, o.organizerID)
	exec(`INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state, per_account_limit) VALUES ($1, $2, $3, '2026-11-20', 'PUBLISHED', 4)`,
		o.eventID, o.seriesID, venueID)
	exec(`INSERT INTO concerts (event_id) VALUES ($1)`, o.eventID)
	exec(`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`, o.eventID, o.artistID)
	exec(`INSERT INTO series_images (id, organizer_id) VALUES ($1, $2), ($3, $2)`,
		o.coverID, o.organizerID, o.unusedID)
	exec(`INSERT INTO series_cover_images (series_id, series_image_id) VALUES ($1, $2)`, o.seriesID, o.coverID)

	o.stock = seedStockForEvent(t, o.eventID, entity.TicketSaleMethodLottery, 100, 4)
	o.saleID = o.stock.SaleID
	o.scannerID = seedEventScanner(t, o.eventID)
	exec(`INSERT INTO rejected_scans (id, event_id, scanner_id, reason, scanned_at) VALUES ($1, $2, $3, 1, now())`,
		uuid.NewV7().String(), o.eventID, string(o.scannerID))
	return o
}

// seededPurchase is one Order of a deletableOrganizer's Event, with its Ticket
// and Settlement.
type seededPurchase struct {
	orderID      string
	ticketID     string
	settlementID string
}

// seedPurchase seeds a Won lottery entry, an Order in orderStatus with one
// Ticket, and a Settlement in settlementStatus for the organizer's Event. An
// admitted purchase also gets an admission through a Scanner of that Event.
func seedPurchase(t *testing.T, o deletableOrganizer, orderStatus entity.OrderStatus, settlementStatus entity.SettlementStatus, admitted bool) seededPurchase {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := testDB.Pool.Exec(ctx, query, args...)
		require.NoError(t, err)
	}

	userID := seedUser(t, "delete-fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
	p := seededPurchase{ticketID: entity.NewID(), settlementID: entity.NewID()}
	_, p.orderID = insertStockEntry(t, o.stock, userID, orderStatus, entity.LotteryEntryStateWon, 1)
	paidAt := issuedTicketsTime
	ticketStatus := entity.TicketStatusIssued
	if orderStatus == entity.OrderStatusRefunded {
		ticketStatus = entity.TicketStatusVoided
	}
	exec(`INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		p.ticketID, p.orderID, userID, o.eventID, int16(ticketStatus), paidAt)
	exec(`INSERT INTO settlements (id, order_id, organizer_id, event_id, status, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		p.settlementID, p.orderID, o.organizerID, o.eventID, int16(settlementStatus), paidAt)
	exec(`INSERT INTO settlement_splits (settlement_id, payee_organizer_id, amount) VALUES ($1, $2, 2500)`,
		p.settlementID, o.organizerID)
	if admitted {
		exec(`UPDATE tickets SET admit_at = $2 WHERE id = $1`, p.ticketID, paidAt)
		exec(`INSERT INTO admissions (ticket_id, event_id, scanner_id, admitted_at) VALUES ($1, $2, $3, $4)`,
			p.ticketID, o.eventID, string(o.scannerID), paidAt)
	}
	return p
}

// removableEvent is a CANCELLED first-party Event whose every purchase is
// finished: a row in every table that holds a key to the Event's records.
type removableEvent struct {
	deletableOrganizer
	// lottery is the Refunded Order of a Won lottery entry, with a Voided,
	// admitted Ticket and a Reversed Settlement.
	lottery seededPurchase
	// checkoutID is a Completed checkout of a first-come stock with a
	// Refunded Order and a Voided Ticket.
	checkoutID      string
	checkoutOrderID string
	checkoutTicket  string
	// resaleListingID is the Sold listing of the lottery Ticket.
	resaleListingID string
}

// seedRemovableEvent seeds a removableEvent for an Organizer in the given
// status.
func seedRemovableEvent(t *testing.T, status entity.OrganizerStatus) removableEvent {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := testDB.Pool.Exec(ctx, query, args...)
		require.NoError(t, err)
	}

	e := removableEvent{deletableOrganizer: seedDeletableOrganizer(t, status)}
	exec(`UPDATE events SET publish_state = 'CANCELLED' WHERE id = $1`, e.eventID)
	e.lottery = seedPurchase(t, e.deletableOrganizer, entity.OrderStatusRefunded, entity.SettlementStatusReversed, true)
	exec(`INSERT INTO ticket_journeys (user_id, event_id, status) VALUES ($1, $2, 1)`, e.followerID, e.eventID)

	buyerID := saleFan(t)
	at := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	firstCome := seedStockForEvent(t, e.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
	e.checkoutID, e.checkoutOrderID = insertStockCheckout(t, firstCome, buyerID, entity.OrderStatusRefunded, entity.CheckoutStatusCompleted, 1, at, true, nil)
	e.checkoutTicket = entity.NewID()
	exec(`INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		e.checkoutTicket, e.checkoutOrderID, buyerID, e.eventID, int16(entity.TicketStatusVoided), at)
	e.resaleListingID = entity.NewID()
	exec(`INSERT INTO resale_listings (id, ticket_id, user_id, ticket_stock_id, price, status, created_at, sold_at)
		VALUES ($1, $2, $3, $4, 3000, 2, $5, $5)`,
		e.resaleListingID, e.lottery.ticketID, e.followerID, string(e.stock.ID), at)
	return e
}

// rowExists reports whether query (a SELECT 1 … WHERE … bound to arg) finds a row.
func rowExists(t *testing.T, query string, arg any) bool {
	t.Helper()
	var exists bool
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `SELECT EXISTS (`+query+`)`, arg).Scan(&exists))
	return exists
}

// seedEventScanner creates a Scanner for the event and returns its id.
func seedEventScanner(t *testing.T, eventID string) entity.ScannerID {
	t.Helper()
	scanner, err := rdb.NewScannerRepository(testDB).Create(context.Background(), entity.NewScanner(eventID))
	require.NoError(t, err)
	return scanner.ID
}

// assertOrganizerRecords asserts whether the Organizer's own records exist, and
// that the Artist and its follower always remain.
func assertOrganizerRecords(t *testing.T, o deletableOrganizer, wantExist bool) {
	t.Helper()
	checks := map[string]struct {
		query string
		arg   any
	}{
		"organizer":     {`SELECT 1 FROM organizers WHERE id = $1`, o.organizerID},
		"series":        {`SELECT 1 FROM series WHERE id = $1`, o.seriesID},
		"event":         {`SELECT 1 FROM events WHERE id = $1`, o.eventID},
		"performer":     {`SELECT 1 FROM concert_artists WHERE event_id = $1`, o.eventID},
		"ticket sale":   {`SELECT 1 FROM ticket_sales WHERE id = $1`, string(o.saleID)},
		"ticket stock":  {`SELECT 1 FROM ticket_stocks WHERE id = $1`, string(o.stock.ID)},
		"seat type":     {`SELECT 1 FROM seat_types WHERE id = $1`, string(o.stock.SeatType.ID)},
		"cover image":   {`SELECT 1 FROM series_images WHERE id = $1`, o.coverID},
		"unused image":  {`SELECT 1 FROM series_images WHERE id = $1`, o.unusedID},
		"cover link":    {`SELECT 1 FROM series_cover_images WHERE series_id = $1`, o.seriesID},
		"association":   {`SELECT 1 FROM organizer_artists WHERE organizer_id = $1`, o.organizerID},
		"scanner":       {`SELECT 1 FROM scanners WHERE id = $1`, string(o.scannerID)},
		"rejected scan": {`SELECT 1 FROM rejected_scans WHERE event_id = $1`, o.eventID},
	}
	for what, c := range checks {
		assert.Equal(t, wantExist, rowExists(t, c.query, c.arg), what)
	}
	assert.True(t, rowExists(t, `SELECT 1 FROM artists WHERE id = $1`, o.artistID), "artist must remain")
	assert.True(t, rowExists(t, `SELECT 1 FROM followed_artists WHERE user_id = $1`, o.followerID), "follow must remain")
}

func assertPurchaseRecords(t *testing.T, p seededPurchase, wantExist bool) {
	t.Helper()
	assert.Equal(t, wantExist, rowExists(t, `SELECT 1 FROM orders WHERE id = $1`, p.orderID), "order")
	assert.Equal(t, wantExist, rowExists(t, `SELECT 1 FROM tickets WHERE id = $1`, p.ticketID), "ticket")
	assert.Equal(t, wantExist, rowExists(t, `SELECT 1 FROM settlements WHERE id = $1`, p.settlementID), "settlement")
}

func TestOrganizerRepository_Delete(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrganizerRepository(testDB)

	t.Run("deletes a deactivated organizer with a published series", func(t *testing.T) {
		// @spec components/entity/organizer/delete "Organizer with a published Series"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)

		require.NoError(t, repo.Delete(ctx, o.organizerID, false))

		assertOrganizerRecords(t, o, false)
	})

	t.Run("deletes a refunded order with its ticket, reversed settlement and admission", func(t *testing.T) {
		// @spec components/entity/organizer/delete "Refunded purchase"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
		p := seedPurchase(t, o, entity.OrderStatusRefunded, entity.SettlementStatusReversed, true)

		require.NoError(t, repo.Delete(ctx, o.organizerID, false))

		assertOrganizerRecords(t, o, false)
		assertPurchaseRecords(t, p, false)
		assert.False(t, rowExists(t, `SELECT 1 FROM admissions WHERE ticket_id = $1`, p.ticketID), "admission")
		assert.False(t, rowExists(t, `SELECT 1 FROM settlement_splits WHERE settlement_id = $1`, p.settlementID), "split")
	})

	t.Run("deletes a resold ticket's listing with the event", func(t *testing.T) {
		// @spec components/entity/organizer/delete "Resold ticket"
		e := seedRemovableEvent(t, entity.OrganizerStatusDeactivated)

		require.NoError(t, repo.Delete(ctx, e.organizerID, false))

		assertOrganizerRecords(t, e.deletableOrganizer, false)
		assertPurchaseRecords(t, e.lottery, false)
		assert.False(t, rowExists(t, `SELECT 1 FROM resale_listings WHERE id = $1`, e.resaleListingID), "resale listing")
		assert.False(t, rowExists(t, `SELECT 1 FROM checkouts WHERE id = $1`, e.checkoutID), "checkout")
	})

	t.Run("deletes an abandoned application with its entry", func(t *testing.T) {
		// @spec components/entity/organizer/delete "Abandoned application"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
		entryID, orderID := insertStockEntry(t, o.stock, o.followerID, entity.OrderStatusVoid, entity.LotteryEntryStateLost, 1)

		require.NoError(t, repo.Delete(ctx, o.organizerID, false))

		assertOrganizerRecords(t, o, false)
		assert.False(t, rowExists(t, `SELECT 1 FROM orders WHERE id = $1`, orderID), "void order")
		assert.False(t, rowExists(t, `SELECT 1 FROM lottery_entries WHERE id = $1`, entryID), "lost entry")
	})

	t.Run("a dry run removes nothing", func(t *testing.T) {
		o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
		p := seedPurchase(t, o, entity.OrderStatusRefunded, entity.SettlementStatusReversed, false)

		require.NoError(t, repo.Delete(ctx, o.organizerID, true))

		assertOrganizerRecords(t, o, true)
		assertPurchaseRecords(t, p, true)
	})

	t.Run("an unknown id fails with NotFound", func(t *testing.T) {
		// @spec components/entity/organizer/delete "Unknown id"
		err := repo.Delete(ctx, entity.NewID(), false)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	blocked := []struct {
		name  string
		setup func(t *testing.T) (deletableOrganizer, *seededPurchase)
	}{
		{
			// @spec components/entity/organizer/delete "Active Organizer"
			name: "an active organizer",
			setup: func(t *testing.T) (deletableOrganizer, *seededPurchase) {
				return seedDeletableOrganizer(t, entity.OrganizerStatusActive), nil
			},
		},
		{
			// @spec components/entity/organizer/delete "Paid order"
			name: "a paid order",
			setup: func(t *testing.T) (deletableOrganizer, *seededPurchase) {
				o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
				p := seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, true)
				return o, &p
			},
		},
		{
			// @spec components/entity/organizer/delete "Settlement"
			name: "a released settlement of a refunded order",
			setup: func(t *testing.T) (deletableOrganizer, *seededPurchase) {
				o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
				p := seedPurchase(t, o, entity.OrderStatusRefunded, entity.SettlementStatusReleased, false)
				return o, &p
			},
		},
		{
			// @spec components/entity/organizer/delete "Purchase in progress"
			name: "an entered lottery entry",
			setup: func(t *testing.T) (deletableOrganizer, *seededPurchase) {
				o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
				insertStockEntry(t, o.stock, o.followerID, entity.OrderStatusPending, entity.LotteryEntryStateEntered, 1)
				return o, nil
			},
		},
		{
			// @spec components/entity/organizer/delete "Won entry awaiting its order"
			name: "a won lottery entry awaiting its order",
			setup: func(t *testing.T) (deletableOrganizer, *seededPurchase) {
				o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
				insertStockEntry(t, o.stock, o.followerID, entity.OrderStatusPending, entity.LotteryEntryStateWon, 1)
				return o, nil
			},
		},
		{
			name: "an expired checkout whose card authorization is not released",
			setup: func(t *testing.T) (deletableOrganizer, *seededPurchase) {
				o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
				f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
				insertStockCheckout(t, f, o.followerID, entity.OrderStatusVoid, entity.CheckoutStatusExpired, 1, saleWindowStart, true, nil)
				return o, nil
			},
		},
		{
			// @spec components/entity/organizer/delete "Payout account"
			name: "a payout account",
			setup: func(t *testing.T) (deletableOrganizer, *seededPurchase) {
				o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
				_, err := testDB.Pool.Exec(context.Background(),
					`INSERT INTO payout_accounts (organizer_id, provider_account_id, status) VALUES ($1, 'acct_delete_test', 1)`,
					o.organizerID)
				require.NoError(t, err)
				return o, nil
			},
		},
	}
	for _, tt := range blocked {
		t.Run("refuses "+tt.name+" and removes nothing", func(t *testing.T) {
			o, p := tt.setup(t)

			for _, dryRun := range []bool{true, false} {
				err := repo.Delete(ctx, o.organizerID, dryRun)
				assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "dryRun=%v", dryRun)
			}

			assertOrganizerRecords(t, o, true)
			if p != nil {
				assertPurchaseRecords(t, *p, true)
			}
		})
	}
}

// TestOrganizerRepository_DeleteWithTicketSale covers an Organizer whose
// event was sold first come: its sale and ended checkouts are removed with
// it, and a committed checkout's Pending Order blocks the deletion.
func TestOrganizerRepository_DeleteWithTicketSale(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrganizerRepository(testDB)
	start := saleWindowStart.Add(time.Hour)
	released := start.Add(time.Minute)

	t.Run("removes the sale and its ended checkouts", func(t *testing.T) {
		o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
		f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		checkoutID, orderID := insertStockCheckout(t, f, o.followerID, entity.OrderStatusVoid, entity.CheckoutStatusReleased, 2, start, true, &released)

		require.NoError(t, repo.Delete(ctx, o.organizerID, false))

		assert.False(t, rowExists(t, `SELECT 1 FROM ticket_sales WHERE id = $1`, string(f.SaleID)), "ticket sale")
		assert.False(t, rowExists(t, `SELECT 1 FROM ticket_stocks WHERE id = $1`, string(f.ID)), "ticket stock")
		assert.False(t, rowExists(t, `SELECT 1 FROM checkouts WHERE id = $1`, checkoutID), "checkout")
		assert.False(t, rowExists(t, `SELECT 1 FROM orders WHERE id = $1`, orderID), "void order")
	})

	t.Run("refuses a committed checkout's pending order", func(t *testing.T) {
		o := seedDeletableOrganizer(t, entity.OrganizerStatusDeactivated)
		f := seedStockForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		checkoutID, _ := insertStockCheckout(t, f, o.followerID, entity.OrderStatusPending, entity.CheckoutStatusCommitted, 2, start, true, nil)

		err := repo.Delete(ctx, o.organizerID, false)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.True(t, rowExists(t, `SELECT 1 FROM checkouts WHERE id = $1`, checkoutID), "nothing is removed")
		assertOrganizerRecords(t, o, true)
	})
}
