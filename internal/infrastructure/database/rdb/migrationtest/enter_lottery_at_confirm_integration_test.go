package migrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enterLotteryAtConfirmPrevious is the last migration before
// 20261027043053_enter_lottery_at_confirm.sql.
const enterLotteryAtConfirmPrevious = "20261022014600"

// enterLotteryAtConfirmVersion is 20261027043053_enter_lottery_at_confirm.sql.
const enterLotteryAtConfirmVersion = "20261027043053"

// TestMigration_EnterLotteryAtConfirm loads a Draft, a Withdrawn entry with a
// Referral, an Entered entry with a Referral and a Checkout with a Referral,
// applies 20261027043053_enter_lottery_at_confirm.sql and checks that the
// Draft and the Withdrawn entry are gone, the codes moved onto
// lottery_entries.referral_code and checkouts.referral_code, referrals is
// dropped, and the new constraints reject state 5, a NULL card authorization,
// a second entry of a user for a ticket type and a second entry of a card
// authorization.
func TestMigration_EnterLotteryAtConfirm(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(enterLotteryAtConfirmPrevious)

	var (
		venueID           = id(1)
		seriesID          = id(2)
		eventID           = id(3)
		lotterySaleID     = id(4)
		lotteryTypeID     = id(5)
		firstComeSaleID   = id(6)
		firstComeTypeID   = id(7)
		fanA              = id(10)
		fanB              = id(11)
		fanC              = id(12)
		draftID           = id(20)
		withdrawnID       = id(21)
		enteredID         = id(22)
		checkoutID        = id(23)
		withdrawnReferral = id(30)
		enteredReferral   = id(31)
		checkoutReferral  = id(32)
	)
	start := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO venues (id, name) VALUES ($1, 'Zepp Haneda')`, venueID)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour 2026', 'TOUR')`, seriesID)
	mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2026-12-01')`, eventID, seriesID, venueID)
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at) VALUES ($1, $2, 'Lottery', 2, $3, $4)`,
		lotterySaleID, seriesID, start, start.Add(72*time.Hour))
	mustExec(`INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit) VALUES ($1, $2, $3, 8000, 100, 4)`,
		lotteryTypeID, lotterySaleID, eventID)
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at) VALUES ($1, $2, 'First come', 1, $3, $4)`,
		firstComeSaleID, seriesID, start.Add(96*time.Hour), start.Add(120*time.Hour))
	mustExec(`INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit) VALUES ($1, $2, $3, 3000, 100, 4)`,
		firstComeTypeID, firstComeSaleID, eventID)

	mustExec(`INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state) VALUES ($1, $2, $3, 2, 'pi_draft', 5)`,
		draftID, lotteryTypeID, fanA)
	mustExec(`INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state) VALUES ($1, $2, $3, 2, 'pi_withdrawn', 4)`,
		withdrawnID, lotteryTypeID, fanB)
	mustExec(`INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state) VALUES ($1, $2, $3, 2, 'pi_entered', 1)`,
		enteredID, lotteryTypeID, fanC)
	mustExec(`INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, status, hold_expire_at) VALUES ($1, $2, $3, 2, 6000, 1, $4)`,
		checkoutID, firstComeTypeID, fanA, start.Add(97*time.Hour))
	mustExec(`INSERT INTO referrals (id, lottery_entry_id, code, created_at) VALUES ($1, $2, 'member-w', $3)`,
		withdrawnReferral, withdrawnID, start)
	mustExec(`INSERT INTO referrals (id, lottery_entry_id, code, created_at) VALUES ($1, $2, 'member-a', $3)`,
		enteredReferral, enteredID, start)
	mustExec(`INSERT INTO referrals (id, checkout_id, code, created_at) VALUES ($1, $2, 'member-b', $3)`,
		checkoutReferral, checkoutID, start)

	apply(enterLotteryAtConfirmVersion)

	t.Run("the Draft and the Withdrawn entry are deleted", func(t *testing.T) {
		rows, err := conn.Query(ctx, `SELECT id::text FROM lottery_entries ORDER BY id`)
		require.NoError(t, err)
		var got []string
		for rows.Next() {
			var entryID string
			require.NoError(t, rows.Scan(&entryID))
			got = append(got, entryID)
		}
		require.NoError(t, rows.Err())
		assert.Equal(t, []string{enteredID}, got)
	})

	t.Run("the codes move onto the entry and the checkout", func(t *testing.T) {
		var entryCode, checkoutCode string
		require.NoError(t, conn.QueryRow(ctx, `SELECT referral_code FROM lottery_entries WHERE id = $1`, enteredID).Scan(&entryCode))
		require.NoError(t, conn.QueryRow(ctx, `SELECT referral_code FROM checkouts WHERE id = $1`, checkoutID).Scan(&checkoutCode))
		assert.Equal(t, "member-a", entryCode)
		assert.Equal(t, "member-b", checkoutCode)
	})

	t.Run("referrals is dropped", func(t *testing.T) {
		var exists bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT to_regclass('referrals') IS NOT NULL`).Scan(&exists))
		assert.False(t, exists)
	})

	t.Run("state 5 is rejected", func(t *testing.T) {
		_, err := conn.Exec(ctx, `UPDATE lottery_entries SET state = 5 WHERE id = $1`, enteredID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_lottery_entries_state")
	})

	t.Run("a NULL card authorization is rejected", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, state) VALUES ($1, $2, $3, 1, 1)`,
			id(24), lotteryTypeID, fanA)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "card_authorization_id")
	})

	t.Run("a second entry of the user for the ticket type is rejected in any state", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state) VALUES ($1, $2, $3, 1, 'pi_lost', 3)`,
			id(25), lotteryTypeID, fanC)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uq_lottery_entries_ticket_type_user")
	})

	t.Run("a second entry of the card authorization is rejected", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state) VALUES ($1, $2, $3, 1, 'pi_entered', 1)`,
			id(26), lotteryTypeID, fanA)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uq_lottery_entries_card_authorization_id")
	})

	t.Run("a malformed referral code is rejected", func(t *testing.T) {
		_, err := conn.Exec(ctx, `UPDATE checkouts SET referral_code = 'Member_A' WHERE id = $1`, checkoutID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_checkouts_referral_code")
	})
}
