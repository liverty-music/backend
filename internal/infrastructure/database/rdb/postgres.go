package rdb

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"cloud.google.com/go/cloudsqlconn"
	"github.com/jackc/pgx/v5/pgxpool"
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
func New(ctx context.Context, dbCfg config.DatabaseConfig, isLocal bool, logger *logging.Logger) (*Database, error) {
	dsn := dbCfg.GetDSN()

	// Create pgxpool for direct pgx usage
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pgxpool config: %w", err)
	}
	poolConfig.MaxConns = int32(dbCfg.MaxOpenConns)
	poolConfig.MinConns = int32(dbCfg.MaxIdleConns)

	var dialer *cloudsqlconn.Dialer
	if !isLocal {
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
			return d.Dial(dialCtx, dbCfg.InstanceConnectionName)
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
		slog.String("host", dbCfg.Host),
		slog.Int("port", dbCfg.Port),
		slog.String("database", dbCfg.Name),
		slog.Int("max_open_conns", dbCfg.MaxOpenConns),
		slog.Int("max_idle_conns", dbCfg.MaxIdleConns),
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
