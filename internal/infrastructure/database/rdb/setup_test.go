package rdb_test

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/require"
)

var testDB *rdb.Database

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
		Host:              "localhost",
		Port:              5432,
		Name:              "test-db",
		User:              "test-user",
		SSLMode:           "disable",
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

func cleanDatabase() {
	if testDB == nil {
		testDB = setupTestDatabase()
	}
	cleanTables(testDB)
}

// ptr returns a pointer to a copy of v. Used to construct optional fields in test fixtures.
func ptr[T any](v T) *T { return &v }

// seedUser inserts a minimal user record and returns its ID.
func seedUser(t *testing.T, name, email, externalID string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7()).String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO users (id, name, email, external_id) VALUES ($1, $2, $3, $4)`,
		id, name, email, externalID,
	)
	require.NoError(t, err)
	return id
}

// seedArtist inserts a minimal artist record and returns its ID.
func seedArtist(t *testing.T, name, mbid string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7()).String()
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
	id := uuid.Must(uuid.NewV7()).String()
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
	id := uuid.Must(uuid.NewV7()).String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO homes (id, country_code, level_1) VALUES ($1, $2, $3)`,
		id, countryCode, level1,
	)
	require.NoError(t, err)
	return id
}

// seedEvent inserts a minimal event record and returns its ID.
func seedEvent(t *testing.T, venueID, artistID, title, date string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7()).String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, venue_id, title, local_event_date, artist_id) VALUES ($1, $2, $3, $4, $5)`,
		id, venueID, title, date, artistID,
	)
	require.NoError(t, err)
	return id
}

func cleanTables(db *rdb.Database) {
	ctx := context.Background()
	tables := []string{
		"nullifiers",
		"merkle_tree",
		"tickets",
		"ticket_emails",
		"ticket_journeys",
		"push_subscriptions",
		"latest_search_logs",
		"followed_artists",
		"artist_official_site",
		"concerts",
		"events",
		"artists",
		"venues",
		"homes",
		"users",
	}

	for _, table := range tables {
		_, err := db.Pool.Exec(ctx, "TRUNCATE TABLE "+table+" CASCADE")
		if err != nil {
			panic("Failed to clean table " + table + ": " + err.Error())
		}
	}
}
