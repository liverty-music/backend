package rdb

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/versions/*.sql
var migrationFS embed.FS

// RunMigrations applies pending database migrations using goose v3 Provider API.
// It creates a short-lived *sql.DB via NewStdlibDB, acquires a PostgreSQL advisory
// lock to prevent concurrent execution, and applies all embedded SQL migrations.
// The database connection is closed after migrations complete.
func RunMigrations(ctx context.Context, cfg *config.Config, logger *logging.Logger) error {
	logger.Info(ctx, "Starting database migrations")

	db, cleanup, err := NewStdlibDB(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create migration database connection: %w", err)
	}
	defer cleanup()

	migrations, err := fs.Sub(migrationFS, "migrations/versions")
	if err != nil {
		return fmt.Errorf("failed to create migration sub-filesystem: %w", err)
	}

	sessionLocker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("failed to create postgres session locker: %w", err)
	}

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrations,
		goose.WithSessionLocker(sessionLocker),
	)
	if err != nil {
		return fmt.Errorf("failed to create goose provider: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	if len(results) == 0 {
		logger.Info(ctx, "No pending migrations to apply")
		return nil
	}

	for _, r := range results {
		logger.Info(ctx, "Applied migration",
			slog.String("file", r.Source.Path),
			slog.String("duration", r.Duration.String()),
		)
	}

	logger.Info(ctx, "Database migrations completed",
		slog.Int("applied", len(results)),
	)

	return nil
}
