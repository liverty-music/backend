package rdb_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// newTestDB returns a database of the test's own, holding every migration
// and no rows (see [testutil.NewDatabase]), so the test may run in parallel
// with any other. It allows at most 4 connections: enough for one test, and
// few enough for the tests that run together to stay inside the server's
// max_connections. It is closed when the test ends.
func newTestDB(t *testing.T) *rdb.Database {
	t.Helper()
	clone := testutil.NewDatabase(t)
	logger, err := logging.New()
	require.NoError(t, err)
	db, err := rdb.New(context.Background(), config.DatabaseConfig{
		Host:    "localhost",
		Port:    clone.Port,
		Name:    clone.Name,
		User:    "test-user",
		SSLMode: "disable",
		// Same search_path as production (app, public).
		Schema:            "app",
		MaxOpenConns:      4,
		MaxIdleConns:      2,
		ConnMaxLifetime:   1800,
		MaxConnIdleTime:   600,
		HealthCheckPeriod: 60,
	}, true, logger)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	return db
}

// databaseOf names the database db is connected to, for a test that opens a
// connection of its own to it.
func databaseOf(t *testing.T, db *rdb.Database) testutil.TestDatabase {
	t.Helper()
	var name string
	require.NoError(t, db.Pool.QueryRow(context.Background(), `SELECT current_database()`).Scan(&name))
	return testutil.TestDatabase{Port: testutil.DatabasePort(), Name: name}
}

// seedUser inserts a minimal user record and returns its ID.
func seedUser(t *testing.T, db *rdb.Database, name, email, providerUserID string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO users (id, name, email, provider_user_id, preferred_language) VALUES ($1, $2, $3, $4, 'ja')`,
		id, name, email, providerUserID,
	)
	require.NoError(t, err)
	return id
}

// seedArtist inserts a minimal artist record and returns its ID.
func seedArtist(t *testing.T, db *rdb.Database, name, mbid string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO artists (id, name, mbid) VALUES ($1, $2, $3)`,
		id, name, mbid,
	)
	require.NoError(t, err)
	return id
}

// seedVenue inserts a minimal venue record and returns its ID.
func seedVenue(t *testing.T, db *rdb.Database, name string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO venues (id, name) VALUES ($1, $2)`,
		id, name,
	)
	require.NoError(t, err)
	return id
}

// seedHome inserts a minimal home record and returns its ID.
func seedHome(t *testing.T, db *rdb.Database, countryCode, level1 string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := db.Pool.Exec(ctx,
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
func seedEvent(t *testing.T, db *rdb.Database, venueID, artistID, title, date string) string {
	t.Helper()
	ctx := context.Background()
	seriesID := uuid.NewV7().String()
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO series (id, title, type) VALUES ($1, $2, 'SINGLE')`,
		seriesID, title,
	)
	require.NoError(t, err)
	eventID := uuid.NewV7().String()
	_, err = db.Pool.Exec(ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, $4)`,
		eventID, seriesID, venueID, date,
	)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `INSERT INTO concerts (event_id) VALUES ($1)`, eventID)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx,
		`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`,
		eventID, artistID,
	)
	require.NoError(t, err)
	return eventID
}

// saleWindowStart is the sale start of every seeded sale; tests read the sale
// inside its window.
var saleWindowStart = time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)

// stockFixture is a seeded TicketStock with the sale seeded to sell it.
type stockFixture struct {
	ID       entity.TicketStockID
	SaleID   entity.TicketSaleID
	EventID  string
	SeatType *entity.SeatType
	Quantity int
	// Limit is the per-account limit of the stock's event.
	Limit int
}

// Price is the price of one ticket of the stock.
func (f *stockFixture) Price() int64 { return f.SeatType.Price }

// seedStock creates, for a new published event, a seat type of 3000 yen, a
// stock of quantity tickets of it, and a TicketSale of the given method
// naming the event, open from saleWindowStart for 20 days; the event's
// per-account limit becomes limit.
func seedStock(t *testing.T, db *rdb.Database, method entity.TicketSaleMethod, quantity, limit int) *stockFixture {
	t.Helper()
	return seedStockForEvent(t, db, seedPublishedEvent(t, db), method, quantity, limit)
}

// seedStockForEvent creates, for the event, a new seat type of 3000 yen, a
// stock of quantity tickets of it, and a TicketSale of the given method
// naming the event, open from saleWindowStart for 20 days; the event's
// per-account limit becomes limit (a discovered event keeps none).
func seedStockForEvent(t *testing.T, db *rdb.Database, eventID string, method entity.TicketSaleMethod, quantity, limit int) *stockFixture {
	t.Helper()
	ctx := context.Background()
	var seriesID string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`UPDATE events SET per_account_limit = CASE WHEN publish_state IS NULL THEN NULL ELSE $2::int END WHERE id = $1 RETURNING series_id`, eventID, limit).Scan(&seriesID))

	// The random tail of a UUIDv7 keeps names unique within a Series; its
	// head is the timestamp.
	suffix := entity.NewID()
	seatType := entity.NewSeatType(seriesID, "S席 "+suffix[len(suffix)-8:], 3000)
	_, err := db.Pool.Exec(ctx, `INSERT INTO seat_types (id, series_id, name, price) VALUES ($1, $2, $3, $4)`,
		string(seatType.ID), seriesID, seatType.Name, seatType.Price)
	require.NoError(t, err)
	stockID := entity.TicketStockID(entity.NewID())
	_, err = db.Pool.Exec(ctx, `INSERT INTO ticket_stocks (id, event_id, seat_type_id, quantity) VALUES ($1, $2, $3, $4)`,
		string(stockID), eventID, string(seatType.ID), quantity)
	require.NoError(t, err)
	saleID := seedSale(t, db, eventID, method, saleWindowStart, saleWindowStart.Add(20*24*time.Hour))
	return &stockFixture{ID: stockID, SaleID: saleID, EventID: eventID, SeatType: seatType, Quantity: quantity, Limit: limit}
}

// seedSale creates a TicketSale of the given method and window naming the
// event.
func seedSale(t *testing.T, db *rdb.Database, eventID string, method entity.TicketSaleMethod, start, end time.Time) entity.TicketSaleID {
	t.Helper()
	ctx := context.Background()
	name := "一般発売"
	if method == entity.TicketSaleMethodLottery {
		name = "抽選"
	}
	id := entity.NewID()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at)
		SELECT $1, series_id, $3, $4, $5, $6 FROM events WHERE id = $2
	`, id, eventID, name, int16(method), start, end)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `INSERT INTO ticket_sale_events (ticket_sale_id, event_id) VALUES ($1, $2)`, id, eventID)
	require.NoError(t, err)
	return entity.TicketSaleID(id)
}

// seedPaidOrder stores a Paid order of n tickets of the stock for the user,
// paid at payTime, with the Won lottery entry in the stock's sale that names
// it, and returns the order's id. It stands in for a won and issued entry.
func seedPaidOrder(t *testing.T, db *rdb.Database, f *stockFixture, userID string, n int, payTime time.Time) entity.OrderID {
	t.Helper()
	ctx := context.Background()
	orderID := entity.NewID()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, card_authorization_id, provider,
			provider_payment_id, status, amount, currency, pay_at)
		VALUES ($1, $2, $3, $4, $5, 1, $6, 1, $7, 'JPY', $8)`,
		orderID, userID, string(f.ID), n, newCardAuthorizationID(db), "pi_"+orderID, f.Price()*int64(n), payTime)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `
		INSERT INTO lottery_entries (id, state, order_id, ticket_sale_id) VALUES ($1, 2, $2, $3)`,
		entity.NewID(), orderID, string(f.SaleID))
	require.NoError(t, err)
	return entity.OrderID(orderID)
}

// issuedTicketsTime is the issue time of the first ticket seedIssuedTickets
// issues; each next ticket is one second later.
var issuedTicketsTime = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// seedIssuedTickets issues n Issued tickets of eventID to userID, through a
// new lottery stock of the event, a Won lottery entry and a Paid order, and
// returns their ids in issue order.
func seedIssuedTickets(t *testing.T, db *rdb.Database, eventID, userID string, n int) []entity.TicketID {
	t.Helper()
	f := seedStockForEvent(t, db, eventID, entity.TicketSaleMethodLottery, 100, 10)
	return seedIssuedTicketsOf(t, db, f, userID, n)
}

// seedIssuedTicketsOf issues n Issued tickets of the stock to userID through
// a Paid order, and returns their ids in issue order.
func seedIssuedTicketsOf(t *testing.T, db *rdb.Database, f *stockFixture, userID string, n int) []entity.TicketID {
	t.Helper()
	orderID := seedPaidOrder(t, db, f, userID, n, issuedTicketsTime)
	ids := make([]entity.TicketID, 0, n)
	for i := range n {
		id := entity.NewID()
		_, err := db.Pool.Exec(context.Background(), `
			INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at)
			VALUES ($1, $2, $3, $4, 1, $5)`,
			id, string(orderID), userID, f.EventID, issuedTicketsTime.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
		ids = append(ids, entity.TicketID(id))
	}
	return ids
}

// setStockSold makes sold tickets of the stock sold, through Paid orders of
// other fans, standing in for won and issued entries.
func setStockSold(t *testing.T, db *rdb.Database, f *stockFixture, sold int) {
	t.Helper()
	for sold > 0 {
		n := min(sold, 10)
		seedPaidOrder(t, db, f, entity.NewID(), n, issuedTicketsTime)
		sold -= n
	}
}

// cleanTables empties every table of the test's database. A subtest that
// needs an empty database calls it instead of asking for a database of its
// own: the repositories of the test stay connected to the same database, and
// emptying it is faster than copying the template again.
func cleanTables(t *testing.T, db *rdb.Database) {
	t.Helper()
	tables := []string{
		"identity_verifications",
		"lottery_entries",
		"checkouts",
		"orders",
		"ticket_stocks",
		"seat_types",
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

	// One statement for every table: a TRUNCATE per table spent 181 of the
	// package's 271 seconds (480 calls, measured 2026-11-04); one statement
	// spent 28.
	_, err := db.Pool.Exec(context.Background(), "TRUNCATE TABLE "+strings.Join(tables, ", ")+" CASCADE")
	require.NoError(t, err)
}

// orderRepository is the Order's repository over orders with the no-op
// payment provider: these tests never refund an Order or read its charge.
func orderRepository(t *testing.T, orders *rdb.OrderRepository) entity.OrderRepository {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return repository.NewOrderRepository(orders, payment.NewNoopOrderPayments(logger))
}

// newCardAuthorizationID records a card authorization whose payment
// provider's ID is "pi_<id>" and returns its id, for fixtures whose Order
// names one.
func newCardAuthorizationID(db *rdb.Database) string {
	id := entity.NewID()
	if _, err := db.Pool.Exec(context.Background(),
		`INSERT INTO card_authorizations (id, provider_card_authorization_id) VALUES ($1, $2)`, id, "pi_"+id); err != nil {
		panic("failed to record a card authorization: " + err.Error())
	}
	return id
}

// providerCardAuthorizationID returns the payment provider's ID of the card
// authorization with the id, for tests that call the payment provider
// directly; an id that is already the provider's is returned as is.
func providerCardAuthorizationID(t *testing.T, db *rdb.Database, id string) string {
	t.Helper()
	if strings.HasPrefix(id, "pi_") {
		return id
	}
	var providerID string
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT provider_card_authorization_id FROM card_authorizations WHERE id = $1`, id).Scan(&providerID))
	return providerID
}
