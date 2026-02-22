package rdb

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"time"

	"cloud.google.com/go/cloudsqlconn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
)

// Database represents the database instance.
type Database struct {
	Pool   *pgxpool.Pool
	logger *logging.Logger
	dialer *cloudsqlconn.Dialer
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

	var dialer *cloudsqlconn.Dialer
	if !cfg.IsLocal() {
		opts := []cloudsqlconn.Option{
			cloudsqlconn.WithIAMAuthN(),
			// Use Private Service Connect (PSC) for non-local environments (dev, stg, prod)
			cloudsqlconn.WithDefaultDialOptions(cloudsqlconn.WithPSC()),
		}

		d, err := cloudsqlconn.NewDialer(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create cloud sql connector dialer: %w", err)
		}
		dialer = d

		// Configure pgx to use the dialer
		poolConfig.ConnConfig.DialFunc = func(dialCtx context.Context, _ string, _ string) (net.Conn, error) {
			return d.Dial(dialCtx, cfg.Database.InstanceConnectionName)
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		if dialer != nil {
			_ = dialer.Close()
		}
		return nil, fmt.Errorf("failed to create pgxpool: %w", err)
	}

	database := &Database{
		Pool:   pool,
		logger: logger,
		dialer: dialer,
	}

	if err := database.Ping(ctx); err != nil {
		_ = database.Close()
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

// NewStdlibDB creates a *sql.DB using pgx/v5/stdlib with the same cloudsqlconn.Dialer
// configuration as the main pool. This short-lived connection is used exclusively
// for running goose migrations and should be closed by the caller after use.
func NewStdlibDB(ctx context.Context, cfg *config.Config, logger *logging.Logger) (*sql.DB, error) {
	connConfig, err := pgx.ParseConfig(cfg.Database.GetDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to parse pgx config for migrations: %w", err)
	}

	if !cfg.IsLocal() {
		d, err := cloudsqlconn.NewDialer(ctx,
			cloudsqlconn.WithIAMAuthN(),
			cloudsqlconn.WithDefaultDialOptions(cloudsqlconn.WithPSC()),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create cloud sql connector dialer for migrations: %w", err)
		}

		connConfig.DialFunc = func(dialCtx context.Context, _ string, _ string) (net.Conn, error) {
			return d.Dial(dialCtx, cfg.Database.InstanceConnectionName)
		}
	}

	db := stdlib.OpenDB(*connConfig)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping database for migrations: %w", err)
	}

	logger.Info(ctx, "Migration database connection established",
		slog.String("host", cfg.Database.Host),
		slog.String("database", cfg.Database.Name),
	)

	return db, nil
}

// Close closes the database connection.
func (d *Database) Close() error {
	d.logger.Info(context.Background(), "Closing database connection")
	if d.Pool != nil {
		d.Pool.Close()
	}
	if d.dialer != nil {
		if err := d.dialer.Close(); err != nil {
			d.logger.Error(context.Background(), "Failed to close database dialer", err)
			return err
		}
	}

	return nil
}
