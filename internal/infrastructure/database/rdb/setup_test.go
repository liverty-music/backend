package rdb_test

import (
	"context"
	"flag"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/require"
	"uuid"
)

var testDB *rdb.Database

// testDBPort is the port of the integration-test PostgreSQL: TEST_DB_PORT when
// set (to run against a throwaway container next to the shared one), else
// the compose service's 15432.
func testDBPort() int {
	if port, err := strconv.Atoi(os.Getenv("TEST_DB_PORT")); err == nil {
		return port
	}
	return 15432
}

func TestMain(m *testing.M) {
	if !flag.Parsed() {
		flag.Parse()
	}

	testDB = setupTestDatabase()

	// Run tests
	code := m.Run()

	// Clean up database if it was initialized
	if testDB != nil {
		if err := testDB.Close(); err != nil {
			panic("Failed to close test database: " + err.Error())
		}
	}

	os.Exit(code)
}

func setupTestDatabase() *rdb.Database {
	dbCfg := config.DatabaseConfig{
		Host:    "localhost",
		Port:    testDBPort(),
		Name:    "test-db",
		User:    "test-user",
		SSLMode: "disable",
		// Same search_path as production (app, public): migrations that set
		// search_path create their tables in app, older ones in public.
		Schema:            "app",
		MaxOpenConns:      10,
		MaxIdleConns:      2,
		ConnMaxLifetime:   1800,
		MaxConnIdleTime:   600,
		HealthCheckPeriod: 60,
	}

	logger, _ := logging.New()
	ctx := context.Background()

	// Create database connection using rdb.New()
	db, err := rdb.New(ctx, dbCfg, true, logger)
	if err != nil {
		panic("Failed to connect to test database: " + err.Error())
	}

	cleanTables(db)

	return db
}

func cleanDatabase(t *testing.T) {
	t.Helper()
	if testDB == nil {
		testDB = setupTestDatabase()
	}
	cleanTables(testDB)
}

// seedUser inserts a minimal user record and returns its ID.
func seedUser(t *testing.T, name, email, providerUserID string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO users (id, name, email, provider_user_id) VALUES ($1, $2, $3, $4)`,
		id, name, email, providerUserID,
	)
	require.NoError(t, err)
	return id
}

// seedArtist inserts a minimal artist record and returns its ID.
func seedArtist(t *testing.T, name, mbid string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO artists (id, name, mbid) VALUES ($1, $2, $3)`,
		id, name, mbid,
	)
	require.NoError(t, err)
	return id
}

// seedVenue inserts a minimal venue record and returns its ID.
func seedVenue(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO venues (id, name) VALUES ($1, $2)`,
		id, name,
	)
	require.NoError(t, err)
	return id
}

// seedHome inserts a minimal home record and returns its ID.
func seedHome(t *testing.T, countryCode, level1 string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO homes (id, country_code, level_1) VALUES ($1, $2, $3)`,
		id, countryCode, level1,
	)
	require.NoError(t, err)
	return id
}

// seedEvent inserts a minimal series + event record and links the given artist
// via concert_artists. Returns the event ID.
//
// The title argument is stored on the series row (1:1 SINGLE series per event).
// artistID is linked via concert_artists, matching the new schema.
func seedEvent(t *testing.T, venueID, artistID, title, date string) string {
	t.Helper()
	ctx := context.Background()
	seriesID := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO series (id, title, type) VALUES ($1, $2, 'SINGLE')`,
		seriesID, title,
	)
	require.NoError(t, err)
	eventID := uuid.NewV7().String()
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, $4)`,
		eventID, seriesID, venueID, date,
	)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx, `INSERT INTO concerts (event_id) VALUES ($1)`, eventID)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`,
		eventID, artistID,
	)
	require.NoError(t, err)
	return eventID
}

// saleWindowStart is the sale start of every seeded sale; tests read the sale
// inside its window.
var saleWindowStart = time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)

// seedTicketType creates, for a new published event, a TicketSale of the
// given method over the event's Series, open from saleWindowStart for 20
// days, offering one TicketType of quantity tickets at 3000 yen.
func seedTicketType(t *testing.T, method entity.TicketSaleMethod, quantity, limit int) *entity.TicketType {
	t.Helper()
	return seedTicketTypeForEvent(t, seedPublishedEvent(t), method, quantity, limit)
}

// seedTicketTypeForEvent creates a TicketSale of the given method over the
// event's Series, open from saleWindowStart for 20 days, offering one
// TicketType of quantity tickets at 3000 yen for the event.
func seedTicketTypeForEvent(t *testing.T, eventID string, method entity.TicketSaleMethod, quantity, limit int) *entity.TicketType {
	t.Helper()
	ctx := context.Background()
	var seriesID string
	require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT series_id FROM events WHERE id = $1`, eventID).Scan(&seriesID))

	name := "一般発売"
	if method == entity.TicketSaleMethodLottery {
		name = "抽選"
	}
	sale := entity.NewTicketSale(seriesID, name, method, saleWindowStart, saleWindowStart.Add(20*24*time.Hour))
	_, err := testDB.Pool.Exec(ctx, `
		INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, string(sale.ID), seriesID, sale.Name, int16(sale.Method), sale.SaleStartTime, sale.SaleEndTime)
	require.NoError(t, err)

	tt := entity.NewTicketType(sale, eventID, 3000, quantity, limit)
	_, err = testDB.Pool.Exec(ctx, `
		INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, string(tt.ID), string(tt.TicketSaleID), tt.EventID, tt.Price, tt.Quantity, tt.PerAccountLimit)
	require.NoError(t, err)
	return tt
}

// issuedTicketsTime is the issue time of the first ticket seedIssuedTickets
// issues; each next ticket is one second later.
var issuedTicketsTime = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// seedIssuedTickets issues n Issued tickets of eventID to userID, through a
// lottery TicketType of the event, a Won lottery entry and a Paid order, and
// returns their ids in issue order.
func seedIssuedTickets(t *testing.T, eventID, userID string, n int) []entity.TicketID {
	t.Helper()
	ctx := context.Background()
	tt := seedTicketTypeForEvent(t, eventID, entity.TicketSaleMethodLottery, 100, 10)

	entryID := entity.NewID()
	_, err := testDB.Pool.Exec(ctx, `
		INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state)
		VALUES ($1, $2, $3, $4, $5, 2)`,
		entryID, string(tt.ID), userID, n, "pi_entry_"+entryID)
	require.NoError(t, err)

	orderID := entity.NewID()
	_, err = testDB.Pool.Exec(ctx, `
		INSERT INTO orders (id, user_id, lottery_entry_id, provider, provider_payment_id, status, amount, currency, pay_at)
		VALUES ($1, $2, $3, 1, $4, 1, $5, 'JPY', $6)`,
		orderID, userID, entryID, "pi_"+orderID, 3000*n, issuedTicketsTime)
	require.NoError(t, err)

	ids := make([]entity.TicketID, 0, n)
	for i := range n {
		id := entity.NewID()
		_, err := testDB.Pool.Exec(ctx, `
			INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at)
			VALUES ($1, $2, $3, $4, 1, $5)`,
			id, orderID, userID, eventID, issuedTicketsTime.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
		ids = append(ids, entity.TicketID(id))
	}
	return ids
}

// setTicketTypeSold sets a ticket type's sold count directly, standing in for
// committed checkouts.
func setTicketTypeSold(t *testing.T, id entity.TicketTypeID, sold int) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(), `UPDATE ticket_types SET sold_count = $2 WHERE id = $1`, string(id), sold)
	require.NoError(t, err)
}

func cleanTables(db *rdb.Database) {
	ctx := context.Background()
	tables := []string{
		"identity_verifications",
		"lottery_entries",
		"ticket_types",
		"ticket_sales",
		"ticket_journeys",
		"push_subscriptions",
		"concert_searches",
		"followed_artists",
		"artist_official_site",
		"ticket_sale_reminders",
		"ticket_sale_searches",
		"concert_artists",
		"concerts",
		"events",
		"series_cover_images",
		"series_images",
		"series",
		"concert_candidates",
		"rejected_concerts",
		"suppressed_concerts",
		"artists",
		"venues",
		"homes",
		"users",
		"organizers",
	}

	for _, table := range tables {
		_, err := db.Pool.Exec(ctx, "TRUNCATE TABLE "+table+" CASCADE")
		if err != nil {
			panic("Failed to clean table " + table + ": " + err.Error())
		}
	}
}

// orderRepository is the Order's repository over testDB's rows with the no-op
// payment provider: these tests never refund an Order or read its charge.
func orderRepository(t *testing.T, orders *rdb.OrderRepository) entity.OrderRepository {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return repository.NewOrderRepository(orders, payment.NewNoopOrderPayments(logger))
}
