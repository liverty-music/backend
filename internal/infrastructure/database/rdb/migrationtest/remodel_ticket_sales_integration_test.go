package migrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// remodelTicketSalesPrevious is the last migration before
// 20261029015006_remodel_ticket_sales.sql.
const remodelTicketSalesPrevious = "20261027120000"

// remodelTicketSalesVersion is 20261029015006_remodel_ticket_sales.sql.
const remodelTicketSalesVersion = "20261029015006"

// TestMigration_RemodelTicketSales loads an Organizer's lottery sale with a
// won entry and its Order, and a first-come sale with a completed checkout and
// its Order, each Order with Tickets and a Held Settlement, a resale listing of
// one first-come Ticket and a reminder of each sale; and three discovered
// sales: a lottery with a RESULT_DAY reminder, a first-come sale with an end
// time and one without. It applies 20261029015006_remodel_ticket_sales.sql and
// checks that every Organizer-sale record is gone, the discovered sales with
// an end time are kept and named by method, the one without is gone, the
// RESULT_DAY reminder is gone, the first-party event has a per-account limit
// of 4 and the discovered one none, and the new tables are empty.
func TestMigration_RemodelTicketSales(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(remodelTicketSalesPrevious)

	var (
		organizerID          = id(1)
		venueID              = id(2)
		organizerSeriesID    = id(3)
		organizerEventID     = id(4)
		discoveredSeriesID   = id(5)
		discoveredEventID    = id(6)
		fanID                = id(7)
		lotterySaleID        = id(10)
		lotteryTypeID        = id(11)
		entryID              = id(12)
		lotteryOrderID       = id(13)
		firstComeSaleID      = id(20)
		firstComeTypeID      = id(21)
		checkoutID           = id(22)
		checkoutOrderID      = id(23)
		lotteryTicketID      = id(30)
		firstComeTicketID    = id(31)
		secondTicketID       = id(32)
		lotterySettlementID  = id(40)
		checkoutSettlementID = id(41)
		listingID            = id(50)
		discoveredLotteryID  = id(60)
		discoveredFirstCome  = id(61)
		discoveredOpenEnded  = id(62)
	)
	start := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	eventStart := time.Date(2026, 12, 20, 9, 0, 0, 0, time.UTC)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO organizers (id, name, owner_email, status, short_name) VALUES ($1, 'Label', 'owner@example.com', 2, 'label')`, organizerID)
	mustExec(`INSERT INTO users (id, provider_user_id, name, email) VALUES ($1, 'ext-1', 'Fan', 'fan@example.com')`, fanID)
	mustExec(`INSERT INTO venues (id, name) VALUES ($1, 'Zepp Haneda')`, venueID)
	mustExec(`INSERT INTO series (id, title, type, organizer_id, visibility) VALUES ($1, 'Tour 2026', 'TOUR', $2, 'PUBLIC')`, organizerSeriesID, organizerID)
	mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date, start_at, publish_state) VALUES ($1, $2, $3, '2026-12-20', $4, 'PUBLISHED')`,
		organizerEventID, organizerSeriesID, venueID, eventStart)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Found Tour', 'TOUR')`, discoveredSeriesID)
	mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2026-12-21')`, discoveredEventID, discoveredSeriesID, venueID)

	// The Organizer's lottery: a won entry and its Order.
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at, draw_at) VALUES ($1, $2, 'FC先行', 2, $3, $4, $4)`,
		lotterySaleID, organizerSeriesID, start, start.Add(72*time.Hour))
	mustExec(`INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit) VALUES ($1, $2, $3, 8000, 100, 4)`,
		lotteryTypeID, lotterySaleID, organizerEventID)
	mustExec(`INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state, draw_position, referral_code) VALUES ($1, $2, $3, 1, 'pi_entry', 2, 0, 'member-a')`,
		entryID, lotteryTypeID, fanID)
	mustExec(`INSERT INTO orders (id, user_id, lottery_entry_id, provider, provider_payment_id, status, amount, currency, pay_at) VALUES ($1, $2, $3, 1, 'pi_entry', 1, 8000, 'JPY', $4)`,
		lotteryOrderID, fanID, entryID, start.Add(73*time.Hour))

	// The Organizer's first-come sale: a completed checkout and its Order.
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at) VALUES ($1, $2, '一般発売', 1, $3, $4)`,
		firstComeSaleID, organizerSeriesID, start.Add(96*time.Hour), start.Add(240*time.Hour))
	mustExec(`INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit, sold_count) VALUES ($1, $2, $3, 3000, 2, 2, 2)`,
		firstComeTypeID, firstComeSaleID, organizerEventID)
	mustExec(`INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, card_authorization_id, status, hold_expire_at, commit_at, capture_at) VALUES ($1, $2, $3, 2, 6000, 'pi_checkout', 3, $4, $4, $4)`,
		checkoutID, firstComeTypeID, fanID, start.Add(97*time.Hour))
	mustExec(`INSERT INTO orders (id, user_id, checkout_id, provider, provider_payment_id, status, amount, currency, pay_at) VALUES ($1, $2, $3, 1, 'pi_checkout', 1, 6000, 'JPY', $4)`,
		checkoutOrderID, fanID, checkoutID, start.Add(97*time.Hour))

	for _, ticket := range []struct{ id, orderID string }{
		{lotteryTicketID, lotteryOrderID}, {firstComeTicketID, checkoutOrderID}, {secondTicketID, checkoutOrderID},
	} {
		mustExec(`INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at) VALUES ($1, $2, $3, $4, 1, $5)`,
			ticket.id, ticket.orderID, fanID, organizerEventID, start.Add(98*time.Hour))
	}
	for _, settlement := range []struct{ id, orderID string }{
		{lotterySettlementID, lotteryOrderID}, {checkoutSettlementID, checkoutOrderID},
	} {
		mustExec(`INSERT INTO settlements (id, order_id, organizer_id, event_id, status, created_at) VALUES ($1, $2, $3, $4, 1, $5)`,
			settlement.id, settlement.orderID, organizerID, organizerEventID, start.Add(98*time.Hour))
		mustExec(`INSERT INTO settlement_splits (settlement_id, payee_organizer_id, amount) VALUES ($1, $2, 2000)`, settlement.id, organizerID)
	}
	mustExec(`INSERT INTO resale_listings (id, ticket_id, user_id, ticket_type_id, price, status, created_at) VALUES ($1, $2, $3, $4, 3000, 1, $5)`,
		listingID, firstComeTicketID, fanID, firstComeTypeID, start.Add(99*time.Hour))

	// Discovered sales: a lottery with a result time, a first-come sale with
	// an end time and one that runs until sold out.
	mustExec(`INSERT INTO ticket_sales (id, series_id, method, sale_start_at, sale_end_at, lottery_result_at, discovered_at) VALUES ($1, $2, 2, $3, $4, $4, $3)`,
		discoveredLotteryID, discoveredSeriesID, start, start.Add(72*time.Hour))
	mustExec(`INSERT INTO ticket_sales (id, series_id, method, sale_start_at, sale_end_at, discovered_at) VALUES ($1, $2, 1, $3, $4, $5)`,
		discoveredFirstCome, discoveredSeriesID, start.Add(96*time.Hour), start.Add(120*time.Hour), start)
	mustExec(`INSERT INTO ticket_sales (id, series_id, method, sale_start_at, discovered_at) VALUES ($1, $2, 1, $3, $4)`,
		discoveredOpenEnded, discoveredSeriesID, start.Add(144*time.Hour), start)

	// Reminders: one per Organizer sale, an APPLY_OPEN and a RESULT_DAY of the
	// discovered lottery, and an APPLY_OPEN of the open-ended sale.
	for i, reminder := range []struct {
		saleID string
		stage  int
	}{
		{lotterySaleID, 1}, {firstComeSaleID, 1}, {discoveredLotteryID, 1}, {discoveredLotteryID, 4}, {discoveredOpenEnded, 1},
	} {
		mustExec(`INSERT INTO ticket_sale_reminders (id, user_id, ticket_sale_id, stage) VALUES ($1, $2, $3, $4)`,
			id(70+i), fanID, reminder.saleID, reminder.stage)
	}

	apply(remodelTicketSalesVersion)

	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		require.NoError(t, conn.QueryRow(ctx, sql, args...).Scan(&n), sql)
		return n
	}

	t.Run("every Organizer-sale record is gone", func(t *testing.T) {
		for _, table := range []string{"orders", "checkouts", "lottery_entries", "tickets", "settlements", "settlement_splits", "resale_listings"} {
			assert.Zero(t, count(`SELECT count(*) FROM `+table), table)
		}
		assert.Zero(t, count(`SELECT count(*) FROM ticket_sales WHERE series_id = $1`, organizerSeriesID))
		assert.Zero(t, count(`SELECT count(*) FROM ticket_sale_reminders WHERE ticket_sale_id = ANY($1::uuid[])`, []string{lotterySaleID, firstComeSaleID}))
		assert.Zero(t, count(`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'app' AND table_name = 'ticket_types'`))
	})

	t.Run("the series, its event and the fan stay", func(t *testing.T) {
		assert.Equal(t, 1, count(`SELECT count(*) FROM series WHERE id = $1`, organizerSeriesID))
		assert.Equal(t, 1, count(`SELECT count(*) FROM users WHERE id = $1`, fanID))
		var limit int
		require.NoError(t, conn.QueryRow(ctx, `SELECT per_account_limit FROM events WHERE id = $1`, organizerEventID).Scan(&limit))
		assert.Equal(t, 4, limit)
		assert.Equal(t, 1, count(`SELECT count(*) FROM events WHERE id = $1 AND per_account_limit IS NULL`, discoveredEventID))
	})

	t.Run("discovered sales with an end time stay, named by method", func(t *testing.T) {
		var lotteryName, firstComeName string
		require.NoError(t, conn.QueryRow(ctx, `SELECT name FROM ticket_sales WHERE id = $1`, discoveredLotteryID).Scan(&lotteryName))
		require.NoError(t, conn.QueryRow(ctx, `SELECT name FROM ticket_sales WHERE id = $1`, discoveredFirstCome).Scan(&firstComeName))
		assert.Equal(t, "抽選", lotteryName)
		assert.Equal(t, "先着", firstComeName)
		assert.Zero(t, count(`SELECT count(*) FROM ticket_sales WHERE id = $1`, discoveredOpenEnded))
	})

	t.Run("only the RESULT_DAY reminder of a kept sale is gone", func(t *testing.T) {
		assert.Equal(t, 1, count(`SELECT count(*) FROM ticket_sale_reminders`))
		assert.Equal(t, 1, count(`SELECT count(*) FROM ticket_sale_reminders WHERE ticket_sale_id = $1 AND stage = 1`, discoveredLotteryID))
	})

	t.Run("the new tables are empty", func(t *testing.T) {
		for _, table := range []string{"seat_types", "ticket_stocks", "ticket_sale_events"} {
			assert.Zero(t, count(`SELECT count(*) FROM `+table), table)
		}
	})

	t.Run("the new constraints hold", func(t *testing.T) {
		_, err := conn.Exec(ctx, `UPDATE events SET per_account_limit = 11 WHERE id = $1`, organizerEventID)
		assert.Error(t, err, "a limit above 10")
		_, err = conn.Exec(ctx, `UPDATE events SET per_account_limit = 4 WHERE id = $1`, discoveredEventID)
		assert.Error(t, err, "a limit on a discovered event")
		_, err = conn.Exec(ctx, `INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, discovered_at) VALUES ($1, $2, '先着', 1, $3, $3)`,
			id(80), discoveredSeriesID, start)
		assert.Error(t, err, "a sale without an end time")
	})
}
