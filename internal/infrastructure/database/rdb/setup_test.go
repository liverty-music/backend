package rdb_test

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
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
	cfg := &config.Config{
		Environment: "local",
		Database: config.DatabaseConfig{
			Host:            "localhost",
			Port:            5432,
			Name:            "test-db",
			User:            "test-user",
			SSLMode:         "disable",
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: 300,
		},
	}

	logger, _ := logging.New()
	ctx := context.Background()

	// Create database connection using rdb.New()
	db, err := rdb.New(ctx, cfg, logger)
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

func cleanTables(db *rdb.Database) {
	ctx := context.Background()
	tables := []string{
		"followed_artists",
		"artist_official_site",
		"concerts",
		"events",
		"artists",
		"venues",
		"users",
	}

	for _, table := range tables {
		_, err := db.Pool.Exec(ctx, "TRUNCATE TABLE "+table+" CASCADE")
		if err != nil {
			panic("Failed to clean table " + table + ": " + err.Error())
		}
	}
}
