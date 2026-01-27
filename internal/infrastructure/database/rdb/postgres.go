package rdb

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
)

// Database represents the database instance.
type Database struct {
	Pool   *pgxpool.Pool
	logger *logging.Logger
}

// New creates a new database instance with connection and ping verification.
func New(ctx context.Context, cfg *config.Config, logger *logging.Logger) (*Database, error) {
	dsn := cfg.Database.GetDSN()

	// Create pgxpool for direct pgx usage
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pgxpool config: %w", err)
	}
	poolConfig.MaxConns = int32(cfg.Database.MaxOpenConns)
	poolConfig.MinConns = int32(cfg.Database.MaxIdleConns)

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create pgxpool: %w", err)
	}

	database := &Database{
		Pool:   pool,
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

	if err := d.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// Close closes the database connection.
func (d *Database) Close() error {
	if d.Pool != nil {
		d.logger.Info(context.Background(), "Closing database connection")
		d.Pool.Close()
	}

	return nil
}
