package rdb_test

import (
	"context"
	"os"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/logging"
	"github.com/uptrace/bun/extra/bundebug"
)

var testDB *rdb.Database

func TestMain(m *testing.M) {
	testDB = setupTestDatabase()
	testDB.AddQueryHook(bundebug.NewQueryHook(
		bundebug.WithVerbose(true),
	))

	// Run tests first to determine if we're in short mode
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
		Database: config.DatabaseConfig{
			Host:            "localhost",
			Port:            5432,
			Name:            "scaffold_test",
			User:            "testuser",
			Password:        "testpassword",
			SSLMode:         "disable",
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: 300,
		},
	}

	logger := logging.New()
	ctx := context.Background()

	// Create database connection using rdb.New()
	db, err := rdb.New(ctx, cfg, logger)
	if err != nil {
		panic("Failed to connect to test database: " + err.Error())
	}

	models := []interface{}{
		(*rdb.User)(nil),
	}

	for _, model := range models {
		_, err := db.DB.NewTruncateTable().
			Model(model).
			Cascade().
			Exec(ctx)
		if err != nil {
			panic("Failed to clean table: " + err.Error())
		}
	}

	return db
}
