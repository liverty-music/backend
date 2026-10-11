// Package migrationtest checks data-moving Atlas migrations: it applies the
// migrations before one to a scratch database, loads rows in the old shape,
// applies the migration and reads the rows back in the new shape. It talks
// SQL only, so it does not depend on the application's Go types.
package migrationtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// adminDSNFormat targets the shared test-db of the integration-test
	// PostgreSQL as the superuser test-user; it only creates the scratch
	// database.
	adminDSNFormat = "postgres://test-user@localhost:%d/test-db?sslmode=disable"

	// scratchDSNFormat targets the scratch database with search_path=app, as
	// the Atlas Operator connects (k8s/atlas/base/atlas-migration.yaml).
	scratchDSNFormat = "postgres://test-user@localhost:%d/%s?sslmode=disable&search_path=app"

	scratchDatabase = "migration-test-db"

	// unifyTicketSalesPrevious is the last migration before
	// 20261011000000_unify_ticket_sales.sql.
	unifyTicketSalesPrevious = "20261010020000"

	// unifyTicketSalesVersion is 20261011000000_unify_ticket_sales.sql
	// itself; later migrations rename what it creates.
	unifyTicketSalesVersion = "20261011000000"
)

// testDBPort is the port of the integration-test PostgreSQL: TEST_DB_PORT when
// set, else the compose service's 15432 (same rule as the rdb tests).
func testDBPort() int {
	if port, err := strconv.Atoi(os.Getenv("TEST_DB_PORT")); err == nil {
		return port
	}
	return 15432
}

// id returns a fixed UUIDv7 whose order follows n, so "newest" in the test
// data is explicit rather than dependent on the clock.
func id(n int) string {
	return fmt.Sprintf("019a0000-%04x-7000-8000-000000000000", n)
}

// TestMigration_UnifyTicketSales loads two lottery phases, three applications
// (one withdrawn with the old state 5), one first-come sale whose per-account
// limit exceeds its quantity (as in production) with a completed and a held
// reservation, one order of each source, two tickets, a verified
// identity, an Organizer with seller details, and one user with holder values
// and one without; it then applies 20261011000000_unify_ticket_sales.sql and
// checks the rows in their new tables and columns.
func TestMigration_UnifyTicketSales(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(unifyTicketSalesPrevious)

	var (
		venueID                = id(1)
		seriesID               = id(2)
		eventA                 = id(3)
		eventB                 = id(4)
		userWith               = id(10) // has holder values
		userNone               = id(11) // has none
		organizer              = id(12)
		identityVerificationID = id(13)
		phaseA                 = id(20) // drawn, on eventA
		phaseB                 = id(21) // not drawn, JPKI-only, on eventB
		appWon                 = id(30) // userWith on phaseA, Won
		appLost                = id(31) // userNone on phaseA, Lost
		appWdrawn              = id(32) // userNone on phaseB, Withdrawn (5); userNone's newest
		saleFC                 = id(40) // first-come on eventA
		resvDone               = id(41) // Completed, 2 tickets
		resvHeld               = id(42) // Held, 1 ticket
		orderApp               = id(50)
		orderResv              = id(51)
		ticketA                = id(60)
		ticketB                = id(61)
	)
	openA := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	closeA := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	drawnA := time.Date(2026, 11, 5, 10, 1, 0, 0, time.UTC)
	openB := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	closeB := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	now := time.Now().UTC().Truncate(time.Second)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO venues (id, name) VALUES ($1, 'Zepp Haneda')`, venueID)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour 2026', 'TOUR')`, seriesID)
	mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $3, $4, '2026-12-01'), ($2, $3, $4, '2026-12-02')`,
		eventA, eventB, seriesID, venueID)
	mustExec(`INSERT INTO users (id, external_id, name, email, holder_full_name, holder_phone_number)
	      VALUES ($1, 'ext-with', 'With', 'with@example.com', '山田 花子', '+819011111111')`, userWith)
	mustExec(`INSERT INTO users (id, external_id, name, email) VALUES ($1, 'ext-none', 'None', 'none@example.com')`, userNone)
	mustExec(`INSERT INTO organizers (id, name, operator_email, status, seller_legal_name, seller_representative_name,
	                              seller_address, seller_phone_number, seller_contact_email)
	      VALUES ($1, 'Label', 'op@example.com', 2, '株式会社レーベル', '代表 太郎', '東京都千代田区1-1', '+81312345678', 'contact@example.com')`, organizer)
	mustExec(`INSERT INTO verified_identities (id, user_id, method, pocket_sign_user_id, dedupe_strength, verified_at, status)
	      VALUES ($1, $2, 1, 'ps-user', 1, $3, 1)`, identityVerificationID, userWith, now)

	mustExec(`INSERT INTO lottery_sales_phases (id, event_id, open_at, close_at, ticket_capacity, max_tickets_per_application,
	                                        ticket_price, drawn_at, verification_requirement)
	      VALUES ($1, $2, $3, $4, 100, 4, 8000, $5, 0)`, phaseA, eventA, openA, closeA, drawnA)
	mustExec(`INSERT INTO lottery_sales_phases (id, event_id, open_at, close_at, ticket_capacity, max_tickets_per_application,
	                                        ticket_price, verification_requirement)
	      VALUES ($1, $2, $3, $4, 50, 2, 9000, 2)`, phaseB, eventB, openB, closeB)
	mustExec(`INSERT INTO ticket_applications (id, phase_id, applicant_id, requested_ticket_count, applicant_full_name,
	                                       applicant_phone_number, payment_intent_ref, state, draw_sequence)
	      VALUES ($1, $2, $3, 2, '山田 花子(旧)', '+819099999999', 'pi_won', 2, 0)`, appWon, phaseA, userWith)
	mustExec(`INSERT INTO ticket_applications (id, phase_id, applicant_id, requested_ticket_count, applicant_full_name,
	                                       applicant_phone_number, payment_intent_ref, state, draw_sequence)
	      VALUES ($1, $2, $3, 1, '佐藤 次郎(旧)', '+819022222222', 'pi_lost', 3, 1)`, appLost, phaseA, userNone)
	mustExec(`INSERT INTO ticket_applications (id, phase_id, applicant_id, requested_ticket_count, applicant_full_name,
	                                       applicant_phone_number, payment_intent_ref, state)
	      VALUES ($1, $2, $3, 1, '佐藤 次郎', '+819033333333', 'pi_withdrawn', 5)`, appWdrawn, phaseB, userNone)

	mustExec(`INSERT INTO ticket_sales (id, event_id, method, sale_start_at, sale_end_at, price, quantity, per_account_limit, sold_count)
	      VALUES ($1, $2, 1, $3, $4, 3000, 5, 10, 2)`, saleFC, eventA, openB, closeB)
	mustExec(`INSERT INTO reservations (id, ticket_sale_id, user_id, ticket_count, amount, holder_full_name, holder_phone_number,
	                                authorization_ref, status, hold_expire_at, committed_at, capture_at)
	      VALUES ($1, $2, $3, 2, 6000, '山田 花子', '+819011111111', 'pi_resv', 3, $4, $4, $4)`, resvDone, saleFC, userWith, now)
	mustExec(`INSERT INTO reservations (id, ticket_sale_id, user_id, ticket_count, amount, status, hold_expire_at)
	      VALUES ($1, $2, $3, 1, 3000, 1, $4)`, resvHeld, saleFC, userNone, now.Add(15*time.Minute))

	mustExec(`INSERT INTO orders (id, buyer_id, application_id, provider, payment_intent_ref, status, amount, currency, paid_at)
	      VALUES ($1, $2, $3, 1, 'pi_won', 1, 16000, 'JPY', $4)`, orderApp, userWith, appWon, now)
	mustExec(`INSERT INTO orders (id, buyer_id, reservation_id, provider, payment_intent_ref, status, amount, currency, paid_at)
	      VALUES ($1, $2, $3, 1, 'pi_resv', 1, 6000, 'JPY', $4)`, orderResv, userWith, resvDone, now)
	mustExec(`INSERT INTO tickets (id, order_id, holder_id, event_id, holder_full_name, holder_phone_number, verified_identity_id, status, issued_at)
	      VALUES ($1, $2, $3, $4, '山田 花子(旧)', '+819099999999', $5, 1, $6)`, ticketA, orderApp, userWith, eventA, identityVerificationID, now)
	mustExec(`INSERT INTO tickets (id, order_id, holder_id, event_id, holder_full_name, holder_phone_number, status, issued_at)
	      VALUES ($1, $2, $3, $4, '山田 花子', '+819011111111', 1, $5)`, ticketB, orderResv, userWith, eventA, now)

	apply(unifyTicketSalesVersion)

	t.Run("users keep their holder values or take their newest application's", func(t *testing.T) {
		for _, tc := range []struct {
			user, fullName, phone string
		}{
			{userWith, "山田 花子", "+819011111111"},
			{userNone, "佐藤 次郎", "+819033333333"},
		} {
			var fullName, phone string
			require.NoError(t, conn.QueryRow(ctx, `SELECT full_name, phone_number FROM users WHERE id = $1`, tc.user).Scan(&fullName, &phone))
			assert.Equal(t, tc.fullName, fullName)
			assert.Equal(t, tc.phone, phone)
		}
	})

	t.Run("the first-come sale keeps its window and moves its stock to one ticket type", func(t *testing.T) {
		var (
			series, name   string
			method, verReq int
			start, end     time.Time
			drawn          *time.Time
		)
		require.NoError(t, conn.QueryRow(ctx,
			`SELECT series_id, name, method, sale_start_at, sale_end_at, verification_requirement, drawn_at FROM ticket_sales WHERE id = $1`,
			saleFC).Scan(&series, &name, &method, &start, &end, &verReq, &drawn))
		assert.Equal(t, seriesID, series)
		assert.Equal(t, "一般発売", name)
		assert.Equal(t, 1, method)
		assert.True(t, openB.Equal(start))
		assert.True(t, closeB.Equal(end))
		assert.Equal(t, 0, verReq)
		assert.Nil(t, drawn)

		var (
			event                                string
			price                                int64
			quantity, perAccountLimit, soldCount int
		)
		require.NoError(t, conn.QueryRow(ctx,
			`SELECT event_id, price, quantity, per_account_limit, sold_count FROM ticket_types WHERE ticket_sale_id = $1`,
			saleFC).Scan(&event, &price, &quantity, &perAccountLimit, &soldCount))
		assert.Equal(t, eventA, event)
		assert.Equal(t, int64(3000), price)
		assert.Equal(t, 5, quantity)
		assert.Equal(t, 5, perAccountLimit, "a limit above the quantity is capped at the quantity")
		assert.Equal(t, 2, soldCount, "the first-come ticket type keeps its sold count")
	})

	t.Run("each lottery phase becomes one lottery sale and one ticket type with the phase id", func(t *testing.T) {
		for _, tc := range []struct {
			phase, event              string
			start, end                time.Time
			drawn                     *time.Time
			verReq                    int
			price                     int64
			quantity, perAccountLimit int
		}{
			{phaseA, eventA, openA, closeA, &drawnA, 0, 8000, 100, 4},
			{phaseB, eventB, openB, closeB, nil, 2, 9000, 50, 2},
		} {
			var (
				event, series, name       string
				method, verReq            int
				start, end                time.Time
				drawn                     *time.Time
				price                     int64
				quantity, perAccountLimit int
				soldCount                 int
			)
			require.NoError(t, conn.QueryRow(ctx, `
				SELECT tt.event_id, tt.price, tt.quantity, tt.per_account_limit, tt.sold_count,
				       ts.series_id, ts.name, ts.method, ts.sale_start_at, ts.sale_end_at, ts.verification_requirement, ts.drawn_at
				FROM ticket_types tt JOIN ticket_sales ts ON ts.id = tt.ticket_sale_id
				WHERE tt.id = $1`, tc.phase).
				Scan(&event, &price, &quantity, &perAccountLimit, &soldCount, &series, &name, &method, &start, &end, &verReq, &drawn))
			assert.Equal(t, tc.event, event)
			assert.Equal(t, tc.price, price)
			assert.Equal(t, tc.quantity, quantity)
			assert.Equal(t, tc.perAccountLimit, perAccountLimit)
			assert.Equal(t, 0, soldCount)
			assert.Equal(t, seriesID, series)
			assert.Equal(t, "抽選", name)
			assert.Equal(t, 2, method)
			assert.True(t, tc.start.Equal(start))
			assert.True(t, tc.end.Equal(end))
			assert.Equal(t, tc.verReq, verReq)
			if tc.drawn == nil {
				assert.Nil(t, drawn)
			} else if assert.NotNil(t, drawn) {
				assert.True(t, tc.drawn.Equal(*drawn))
			}
		}
		var sales int
		require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM ticket_sales`).Scan(&sales))
		assert.Equal(t, 3, sales)
	})

	t.Run("applications become lottery entries and Withdrawn 5 becomes 4", func(t *testing.T) {
		for _, tc := range []struct {
			entry, ticketType, user string
			state                   int
			drawPosition            *int64
		}{
			{appWon, phaseA, userWith, 2, new(int64(0))},
			{appLost, phaseA, userNone, 3, new(int64(1))},
			{appWdrawn, phaseB, userNone, 4, nil},
		} {
			var (
				ticketType, user, ref string
				state                 int
				drawPosition          *int64
			)
			require.NoError(t, conn.QueryRow(ctx,
				`SELECT ticket_type_id, user_id, payment_intent_ref, state, draw_position FROM lottery_entries WHERE id = $1`,
				tc.entry).Scan(&ticketType, &user, &ref, &state, &drawPosition))
			assert.Equal(t, tc.ticketType, ticketType)
			assert.Equal(t, tc.user, user)
			assert.NotEmpty(t, ref)
			assert.Equal(t, tc.state, state)
			assert.Equal(t, tc.drawPosition, drawPosition)
		}
	})

	t.Run("reservations become checkouts on the sale's ticket type", func(t *testing.T) {
		var fcType string
		require.NoError(t, conn.QueryRow(ctx, `SELECT id FROM ticket_types WHERE ticket_sale_id = $1`, saleFC).Scan(&fcType))
		for _, tc := range []struct {
			checkout string
			status   int
		}{{resvDone, 3}, {resvHeld, 1}} {
			var ticketType string
			var status int
			require.NoError(t, conn.QueryRow(ctx, `SELECT ticket_type_id, status FROM checkouts WHERE id = $1`, tc.checkout).
				Scan(&ticketType, &status))
			assert.Equal(t, fcType, ticketType)
			assert.Equal(t, tc.status, status)
		}
	})

	t.Run("orders and tickets name the user, the source and the Kyc", func(t *testing.T) {
		var user string
		var entry, checkout *string
		require.NoError(t, conn.QueryRow(ctx, `SELECT user_id, lottery_entry_id, checkout_id FROM orders WHERE id = $1`, orderApp).
			Scan(&user, &entry, &checkout))
		assert.Equal(t, userWith, user)
		assert.Equal(t, &appWon, entry)
		assert.Nil(t, checkout)
		require.NoError(t, conn.QueryRow(ctx, `SELECT user_id, lottery_entry_id, checkout_id FROM orders WHERE id = $1`, orderResv).
			Scan(&user, &entry, &checkout))
		assert.Equal(t, userWith, user)
		assert.Nil(t, entry)
		assert.Equal(t, &resvDone, checkout)

		var identityVerification *string
		require.NoError(t, conn.QueryRow(ctx, `SELECT user_id, kyc_id FROM tickets WHERE id = $1`, ticketA).Scan(&user, &identityVerification))
		assert.Equal(t, userWith, user)
		assert.Equal(t, &identityVerificationID, identityVerification)
		var identityVerificationUser string
		require.NoError(t, conn.QueryRow(ctx, `SELECT user_id FROM kyc WHERE id = $1`, identityVerificationID).Scan(&identityVerificationUser))
		assert.Equal(t, userWith, identityVerificationUser)
	})

	t.Run("organizer seller details become business details", func(t *testing.T) {
		var legal, rep, addr, phone, email string
		require.NoError(t, conn.QueryRow(ctx, `
			SELECT legal_name, representative_name, business_address, business_phone_number, contact_email
			FROM organizers WHERE id = $1`, organizer).Scan(&legal, &rep, &addr, &phone, &email))
		assert.Equal(t, []string{"株式会社レーベル", "代表 太郎", "東京都千代田区1-1", "+81312345678", "contact@example.com"},
			[]string{legal, rep, addr, phone, email})
	})

	t.Run("old tables and copied columns are gone", func(t *testing.T) {
		for _, table := range []string{"lottery_sales_phases", "ticket_applications", "reservations", "verified_identities", "lottery_phase_sales"} {
			var exists bool
			require.NoError(t, conn.QueryRow(ctx, `SELECT to_regclass('app.' || $1) IS NOT NULL`, table).Scan(&exists))
			assert.False(t, exists, table)
		}
		var copies int
		require.NoError(t, conn.QueryRow(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'app' AND column_name IN ('holder_full_name', 'holder_phone_number', 'applicant_full_name', 'applicant_phone_number')`).
			Scan(&copies))
		assert.Zero(t, copies)
	})

	t.Run("a Draft entry needs no authorization and Withdrawn does not block a new entry", func(t *testing.T) {
		_, err := conn.Exec(ctx,
			`INSERT INTO lottery_entries (id, ticket_type_id, user_id, requested_ticket_count, state) VALUES ($1, $2, $3, 1, 5)`,
			id(70), phaseB, userNone)
		require.NoError(t, err)
		_, err = conn.Exec(ctx,
			`INSERT INTO lottery_entries (id, ticket_type_id, user_id, requested_ticket_count, state) VALUES ($1, $2, $3, 1, 1)`,
			id(71), phaseB, userWith)
		assert.Error(t, err, "an Entered entry without an authorization is rejected")
	})
}

// setupScratchDatabase empties the scratch database and returns a connection
// to it and a function that applies the migrations up to a version ("" for
// all). Cleanup empties the scratch database again; it is reused rather than
// dropped, like the grants test's (DROP DATABASE forces a checkpoint).
func setupScratchDatabase(t *testing.T, ctx context.Context) (*pgx.Conn, func(toVersion string)) {
	t.Helper()

	admin, err := pgx.Connect(ctx, fmt.Sprintf(adminDSNFormat, testDBPort()))
	if err != nil {
		t.Skipf("no local database available: %v", err)
	}
	defer func() { assert.NoError(t, admin.Close(ctx)) }()

	atlasPath, err := exec.LookPath("atlas")
	require.NoError(t, err, "atlas CLI is required to apply migrations")
	migrationsDir, err := filepath.Abs("../../../../../k8s/atlas/base/migrations")
	require.NoError(t, err)

	var exists bool
	require.NoError(t, admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, scratchDatabase).Scan(&exists))
	if !exists {
		_, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{scratchDatabase}.Sanitize())
		require.NoError(t, err)
	}

	dsn := fmt.Sprintf(scratchDSNFormat, testDBPort(), scratchDatabase)
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	reset := func() {
		_, err := conn.Exec(ctx, `
			DROP SCHEMA IF EXISTS app CASCADE;
			DROP SCHEMA IF EXISTS atlas_schema_revisions CASCADE;
			DROP SCHEMA IF EXISTS public CASCADE;
			CREATE SCHEMA public;`)
		require.NoError(t, err)
	}
	reset()
	t.Cleanup(func() {
		reset()
		assert.NoError(t, conn.Close(context.Background()))
	})

	apply := func(toVersion string) {
		t.Helper()
		args := []string{"migrate", "apply", "--url", dsn, "--dir", "file://" + migrationsDir}
		if toVersion != "" {
			args = append(args, "--to-version", toVersion)
		}
		out, err := exec.CommandContext(ctx, atlasPath, args...).CombinedOutput()
		require.NoError(t, err, "atlas migrate apply: %s", out)
	}
	return conn, apply
}
