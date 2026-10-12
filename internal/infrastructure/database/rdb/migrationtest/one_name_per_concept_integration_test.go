package migrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration_OneNamePerConcept loads one row into every table that
// 20261012000000_one_name_per_concept.sql renames, in the old shape, applies
// the migration and reads each row back under its new table and column
// names, so every rename keeps its data.
func TestMigration_OneNamePerConcept(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(unifyTicketSalesVersion)

	var (
		venueID                = id(1)
		seriesID               = id(2)
		eventID                = id(3)
		artistID               = id(4)
		userID                 = id(10)
		organizerID            = id(11)
		identityVerificationID = id(12)
		saleID                 = id(20)
		typeID                 = id(21)
		entryID                = id(30)
		checkoutID             = id(31)
		orderID                = id(40)
		ticketID               = id(41)
		settleID               = id(42)
		stagedID               = id(50)
		rejectedID             = id(51)
		mediaID                = id(60)
		linkID                 = id(70)
		scanID                 = id(71)
	)
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO venues (id, name) VALUES ($1, 'Zepp Haneda')`, venueID)
	mustExec(`INSERT INTO artists (id, name, mbid) VALUES ($1, 'Artist', '00000000-0000-0000-0000-000000000001')`, artistID)
	mustExec(`INSERT INTO organizers (id, name, operator_email, status, zitadel_org_id) VALUES ($1, 'Label', 'op@example.com', 2, 'org-zitadel')`, organizerID)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour 2026', 'TOUR')`, seriesID)
	mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2026-12-01')`, eventID, seriesID, venueID)
	mustExec(`INSERT INTO users (id, external_id, name, email, country, time_zone, is_active)
	      VALUES ($1, 'ext-user', 'User', 'user@example.com', 'JP', 'Asia/Tokyo', true)`, userID)
	mustExec(`INSERT INTO kyc (id, user_id, method, pocket_sign_user_id, dedupe_strength, verified_at, status)
	      VALUES ($1, $2, 1, 'ps-user', 1, $3, 1)`, identityVerificationID, userID, at)
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at, drawn_at)
	      VALUES ($1, $2, '抽選', 2, $3, $4, $4)`, saleID, seriesID, at, at.Add(time.Hour))
	mustExec(`INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit)
	      VALUES ($1, $2, $3, 8000, 100, 4)`, typeID, saleID, eventID)
	mustExec(`INSERT INTO lottery_entries (id, ticket_type_id, user_id, requested_ticket_count, payment_intent_ref, state)
	      VALUES ($1, $2, $3, 2, 'pi_entry', 2)`, entryID, typeID, userID)
	mustExec(`INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, authorization_ref,
	                             authorization_released_at, status, hold_expire_at, committed_at)
	      VALUES ($1, $2, $3, 1, 8000, 'pi_checkout', $4, 5, $4, NULL)`, checkoutID, typeID, userID, at)
	mustExec(`INSERT INTO orders (id, user_id, lottery_entry_id, provider, payment_intent_ref, payment_method_ref, status,
	                          amount, currency, paid_at, refund_ref, confirmation_sent_at)
	      VALUES ($1, $2, $3, 1, 'pi_entry', 'pm_card', 2, 16000, 'JPY', $4, 're_refund', $4)`, orderID, userID, entryID, at)
	mustExec(`INSERT INTO tickets (id, order_id, user_id, event_id, kyc_id, status, issued_at, admitted_at)
	      VALUES ($1, $2, $3, $4, $5, 1, $6, $6)`, ticketID, orderID, userID, eventID, identityVerificationID, at)
	mustExec(`INSERT INTO settlements (id, order_id, organizer_id, event_id, charge_ref, status, released_at, settled_at)
	      VALUES ($1, $2, $3, $4, 'ch_charge', 2, $5, $5)`, settleID, orderID, organizerID, eventID, at)
	mustExec(`INSERT INTO settlement_splits (settlement_id, payee_organizer_id, amount, transfer_ref, transfer_reversal_ref)
	      VALUES ($1, $2, 14720, 'tr_payout', 'trr_reversal')`, settleID, organizerID)
	mustExec(`INSERT INTO organizer_connected_accounts (organizer_id, account_ref, status) VALUES ($1, 'acct_payout', 2)`, organizerID)
	mustExec(`INSERT INTO staged_concerts (id, artist_id, series_id, title, local_date, listed_venue_name)
	      VALUES ($1, $2, $3, 'Tour 2026', '2026-12-01', 'Zepp Haneda')`, stagedID, artistID, seriesID)
	mustExec(`INSERT INTO rejected_concerts_log (id, artist_id, artist_name, title, local_date, listed_venue_name, reason)
	      VALUES ($1, $2, 'Artist', 'Tour 2026', '2026-12-01', 'Zepp Haneda', 'duplicate')`, rejectedID, artistID)
	mustExec(`INSERT INTO latest_search_logs (artist_id, searched_at) VALUES ($1, $2)`, artistID, at)
	mustExec(`INSERT INTO media (id, organizer_id, kind, attributes) VALUES ($1, $2, 'IMAGE', '{"content_type": "image/png"}')`, mediaID, organizerID)
	mustExec(`INSERT INTO series_media (series_id, media_id) VALUES ($1, $2)`, seriesID, mediaID)
	mustExec(`INSERT INTO reception_links (id, event_id, number, token_hash, status) VALUES ($1, $2, 1, decode(repeat('ab', 32), 'hex'), 1)`, linkID, eventID)
	mustExec(`INSERT INTO rejected_scans (id, event_id, reception_link_id, ticket_id, reason, scanned_at) VALUES ($1, $2, $3, $4, 5, $5)`,
		scanID, eventID, linkID, ticketID, at)

	apply("")

	readOne := func(t *testing.T, sql string, args []any, dest ...any) {
		t.Helper()
		require.NoError(t, conn.QueryRow(ctx, sql, args...).Scan(dest...), sql)
	}

	t.Run("users and organizers keep their provider IDs", func(t *testing.T) {
		var providerUserID, providerOrganizerID string
		readOne(t, `SELECT provider_user_id FROM users WHERE id = $1`, []any{userID}, &providerUserID)
		readOne(t, `SELECT provider_organizer_id FROM organizers WHERE id = $1`, []any{organizerID}, &providerOrganizerID)
		assert.Equal(t, "ext-user", providerUserID)
		assert.Equal(t, "org-zitadel", providerOrganizerID)

		var dropped int
		readOne(t, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'users' AND column_name IN ('country', 'is_active')`, nil, &dropped)
		assert.Zero(t, dropped, "country and is_active are dropped")
	})

	t.Run("the kyc row is an identity verification", func(t *testing.T) {
		var providerUserID string
		var verifyAt time.Time
		readOne(t, `SELECT provider_user_id, verify_at FROM identity_verifications WHERE id = $1`, []any{identityVerificationID}, &providerUserID, &verifyAt)
		assert.Equal(t, "ps-user", providerUserID)
		assert.True(t, verifyAt.Equal(at))
	})

	t.Run("ticketing rows keep their card authorization, provider and time values", func(t *testing.T) {
		var drawAt time.Time
		readOne(t, `SELECT draw_at FROM ticket_sales WHERE id = $1`, []any{saleID}, &drawAt)
		assert.True(t, drawAt.Equal(at.Add(time.Hour)))

		var entryCount int
		var entryAuth string
		readOne(t, `SELECT ticket_count, card_authorization_id FROM lottery_entries WHERE id = $1`, []any{entryID}, &entryCount, &entryAuth)
		assert.Equal(t, 2, entryCount)
		assert.Equal(t, "pi_entry", entryAuth)

		var checkoutAuth string
		var releaseAt time.Time
		var commitAt *time.Time
		readOne(t, `SELECT card_authorization_id, card_authorization_release_at, commit_at FROM checkouts WHERE id = $1`, []any{checkoutID},
			&checkoutAuth, &releaseAt, &commitAt)
		assert.Equal(t, "pi_checkout", checkoutAuth)
		assert.True(t, releaseAt.Equal(at))
		assert.Nil(t, commitAt)

		var payment, method, refund string
		var payAt, sendAt time.Time
		readOne(t, `SELECT provider_payment_id, provider_payment_method_id, provider_refund_id, pay_at, confirmation_send_at FROM orders WHERE id = $1`,
			[]any{orderID}, &payment, &method, &refund, &payAt, &sendAt)
		assert.Equal(t, []string{"pi_entry", "pm_card", "re_refund"}, []string{payment, method, refund})
		assert.True(t, payAt.Equal(at))
		assert.True(t, sendAt.Equal(at))

		var verificationID string
		var issueAt, admitAt time.Time
		readOne(t, `SELECT identity_verification_id, issue_at, admit_at FROM tickets WHERE id = $1`, []any{ticketID}, &verificationID, &issueAt, &admitAt)
		assert.Equal(t, identityVerificationID, verificationID)
		assert.True(t, issueAt.Equal(at))
		assert.True(t, admitAt.Equal(at))
	})

	t.Run("settlements and payout accounts keep their provider IDs", func(t *testing.T) {
		var charge string
		var releaseAt, createdAt time.Time
		readOne(t, `SELECT provider_charge_id, release_at, created_at FROM settlements WHERE id = $1`, []any{settleID}, &charge, &releaseAt, &createdAt)
		assert.Equal(t, "ch_charge", charge)
		assert.True(t, releaseAt.Equal(at))
		assert.True(t, createdAt.Equal(at))

		var payout, reversal string
		readOne(t, `SELECT provider_payout_id, provider_payout_reversal_id FROM settlement_splits WHERE settlement_id = $1`, []any{settleID}, &payout, &reversal)
		assert.Equal(t, "tr_payout", payout)
		assert.Equal(t, "trr_reversal", reversal)

		var account string
		readOne(t, `SELECT provider_account_id FROM payout_accounts WHERE organizer_id = $1`, []any{organizerID}, &account)
		assert.Equal(t, "acct_payout", account)
	})

	t.Run("concert candidates, rejected concerts and concert searches keep their rows", func(t *testing.T) {
		var candidates, rejected, searches int
		readOne(t, `SELECT count(*) FROM concert_candidates WHERE id = $1`, []any{stagedID}, &candidates)
		readOne(t, `SELECT count(*) FROM rejected_concerts WHERE id = $1`, []any{rejectedID}, &rejected)
		readOne(t, `SELECT count(*) FROM concert_searches WHERE artist_id = $1`, []any{artistID}, &searches)
		assert.Equal(t, []int{1, 1, 1}, []int{candidates, rejected, searches})
	})

	t.Run("the media row is the series image and series cover", func(t *testing.T) {
		var contentType, coverID string
		readOne(t, `SELECT attributes->>'content_type' FROM series_images WHERE id = $1`, []any{mediaID}, &contentType)
		readOne(t, `SELECT series_image_id FROM series_cover_images WHERE series_id = $1`, []any{seriesID}, &coverID)
		assert.Equal(t, "image/png", contentType)
		assert.Equal(t, mediaID, coverID)
	})

	t.Run("a rejected scan with reason 4 is refused", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO rejected_scans (id, event_id, reception_link_id, ticket_id, reason, scanned_at) VALUES ($1, $2, $3, $4, 4, $5)`,
			id(72), eventID, linkID, ticketID, at)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_rejected_scans_reason")
	})
}
