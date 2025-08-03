package rdb

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/logging"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// Database represents the database instance.
type Database struct {
	*bun.DB
	logger *logging.Logger
}

// New creates a new database instance with connection and ping verification.
func New(ctx context.Context, cfg *config.Config, logger *logging.Logger) (*Database, error) {
	// Create PostgreSQL driver
	dsn := cfg.Database.GetDSN()
	driver := pgdriver.NewConnector(pgdriver.WithDSN(dsn))

	sqldb := sql.OpenDB(driver)

	db := bun.NewDB(sqldb, pgdialect.New())

	// Set connection pool settings
	sqldb.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqldb.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqldb.SetConnMaxLifetime(time.Duration(cfg.Database.ConnMaxLifetime) * time.Second)

	database := &Database{
		DB:     db,
		logger: logger,
	}

	if err := database.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logger.Info(ctx, "Database connection established successfully",
		slog.String("host", cfg.Database.Host),
		slog.Int("port", cfg.Database.Port),
		slog.String("database", cfg.Database.Name),
		slog.Int("max_open_conns", cfg.Database.MaxOpenConns),
		slog.Int("max_idle_conns", cfg.Database.MaxIdleConns),
	)

	return database, nil
}

const pingTimeout = 5 * time.Second

// Ping verifies the database connection.
func (d *Database) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := d.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// Close closes the database connection.
func (d *Database) Close() error {
	if d.DB != nil {
		d.logger.Info(context.Background(), "Closing database connection")

		if err := d.DB.Close(); err != nil {
			return fmt.Errorf("failed to close database connection: %w", err)
		}
	}

	return nil
}
