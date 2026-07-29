// Package main provides a one-time migration job that normalizes listed_venue_name
// values in venues, staged_concerts, and events tables by applying entity.NormalizeVenueName.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

func main() {
	if err := run(); err != nil {
		logger, _ := logging.New()
		logger.Error(context.Background(), "normalize-venue-names migration failed", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger, _ := logging.New()
	bootLogger.Info(ctx, "starting normalize-venue-names migration")

	cfg, err := config.Load[config.JobConfig]()
	if err != nil {
		return err
	}

	logger, err := logging.New()
	if err != nil {
		return err
	}
	slog.SetDefault(logger.Slog())

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return err
	}

	shutdown.Init(logger)
	shutdown.AddDatastorePhase(db)

	// Run all three tables inside a single transaction so a mid-migration crash
	// leaves no table in a partially-normalized state.
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// venues must be normalized first: resolveOrCreateVenue looks up venues by
	// their listed_venue_name, so an un-normalized venues row would cause a miss
	// after staged_concerts is normalized (new concerts stage with a normalized
	// name that no longer matches the old raw-value venues row).
	venuesUpdated, err := normalizeVenues(ctx, tx, logger)
	if err != nil {
		return fmt.Errorf("normalize venues: %w", err)
	}

	stagedUpdated, stagedDeleted, err := normalizeStagedConcerts(ctx, tx, logger)
	if err != nil {
		return fmt.Errorf("normalize staged_concerts: %w", err)
	}

	eventsUpdated, err := normalizeEvents(ctx, tx, logger)
	if err != nil {
		return fmt.Errorf("normalize events: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration transaction: %w", err)
	}

	logger.Info(ctx, "normalize-venue-names migration complete",
		slog.Int("venues_updated", venuesUpdated),
		slog.Int("staged_concerts_updated", stagedUpdated),
		slog.Int("staged_concerts_deleted_duplicates", stagedDeleted),
		slog.Int("events_updated", eventsUpdated),
	)

	return nil
}

// normalizeVenues normalizes listed_venue_name in venues. This must run before
// staged_concerts so that the GetByListedName SQL equality lookup keeps working
// after the write-path normalization is deployed: a staged concert with a
// normalized name must find its existing venue row by the same normalized key.
func normalizeVenues(ctx context.Context, tx pgx.Tx, logger *logging.Logger) (int, error) {
	rows, err := tx.Query(ctx, `SELECT id, listed_venue_name FROM venues WHERE listed_venue_name IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("query venues: %w", err)
	}

	type row struct {
		id   string
		name string
	}
	var toUpdate []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan venues row: %w", err)
		}
		normalized := entity.NormalizeVenueName(r.name)
		if normalized != r.name {
			toUpdate = append(toUpdate, row{id: r.id, name: normalized})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate venues rows: %w", err)
	}

	for _, r := range toUpdate {
		if _, err := tx.Exec(ctx, `UPDATE venues SET listed_venue_name = $1 WHERE id = $2`, r.name, r.id); err != nil {
			return 0, fmt.Errorf("update venues row %s: %w", r.id, err)
		}
	}

	logger.Info(ctx, "venues normalization prepared", slog.Int("to_update", len(toUpdate)))
	return len(toUpdate), nil
}

// normalizeStagedConcerts normalizes listed_venue_name in staged_concerts.
//
// Unresolved rows (resolved_place_id IS NULL) are covered by the partial unique
// index uq_staged_concerts_by_listed_name(artist_id, local_date, listed_venue_name).
// Normalizing a prefixed name can produce a value that already exists for another
// row with the same (artist_id, local_date). To avoid a constraint violation we
// first detect such duplicate pairs and delete the un-normalized duplicate (keeping
// the row that already holds, or will hold after the update, the canonical form).
func normalizeStagedConcerts(ctx context.Context, tx pgx.Tx, logger *logging.Logger) (updated, deleted int, err error) {
	rows, err := tx.Query(ctx, `
		SELECT id, artist_id, local_date::text, listed_venue_name, resolved_place_id
		FROM staged_concerts
		WHERE listed_venue_name IS NOT NULL
		ORDER BY id
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("query staged_concerts: %w", err)
	}

	type stagingRow struct {
		id              string
		artistID        string
		localDate       string
		name            string
		resolvedPlaceID *string
	}

	var all []stagingRow
	for rows.Next() {
		var r stagingRow
		if err := rows.Scan(&r.id, &r.artistID, &r.localDate, &r.name, &r.resolvedPlaceID); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan staged_concerts row: %w", err)
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate staged_concerts rows: %w", err)
	}

	type withNorm struct {
		r           stagingRow
		norm        string
		needsUpdate bool
	}

	var unresolved []withNorm
	var resolved []withNorm
	for _, r := range all {
		norm := entity.NormalizeVenueName(r.name)
		wn := withNorm{r: r, norm: norm, needsUpdate: norm != r.name}
		if r.resolvedPlaceID == nil {
			unresolved = append(unresolved, wn)
		} else {
			resolved = append(resolved, wn)
		}
	}

	// Sort unresolved rows: already-normalized rows first so they "claim" the
	// dedup key and the un-normalized duplicates are the ones deleted.
	sort.SliceStable(unresolved, func(i, j int) bool {
		alreadyI := !unresolved[i].needsUpdate
		alreadyJ := !unresolved[j].needsUpdate
		if alreadyI != alreadyJ {
			return alreadyI
		}
		return unresolved[i].r.id < unresolved[j].r.id
	})

	// Detect which unresolved rows would produce a duplicate normalized key.
	type dedupKey struct{ artistID, localDate, norm string }
	seen := make(map[dedupKey]bool)
	var toDelete []string
	var toUpdate []struct{ id, name string }

	for _, wn := range unresolved {
		k := dedupKey{wn.r.artistID, wn.r.localDate, wn.norm}
		if seen[k] {
			// Another row already claims this (artistID, localDate, normalizedName).
			// Delete this row — it is a true dedup candidate after normalization.
			toDelete = append(toDelete, wn.r.id)
		} else {
			seen[k] = true
			if wn.needsUpdate {
				toUpdate = append(toUpdate, struct{ id, name string }{wn.r.id, wn.norm})
			}
		}
	}

	// Resolved rows are not covered by the partial index — update unconditionally.
	for _, wn := range resolved {
		if wn.needsUpdate {
			toUpdate = append(toUpdate, struct{ id, name string }{wn.r.id, wn.norm})
		}
	}

	// Delete duplicates first so subsequent UPDATEs don't collide.
	for _, id := range toDelete {
		if _, err := tx.Exec(ctx, `DELETE FROM staged_concerts WHERE id = $1`, id); err != nil {
			return 0, 0, fmt.Errorf("delete duplicate staged_concerts row %s: %w", id, err)
		}
	}
	for _, r := range toUpdate {
		if _, err := tx.Exec(ctx, `UPDATE staged_concerts SET listed_venue_name = $1 WHERE id = $2`, r.name, r.id); err != nil {
			return 0, 0, fmt.Errorf("update staged_concerts row %s: %w", r.id, err)
		}
	}

	logger.Info(ctx, "staged_concerts normalization prepared",
		slog.Int("to_update", len(toUpdate)),
		slog.Int("to_delete", len(toDelete)),
	)
	return len(toUpdate), len(toDelete), nil
}

// normalizeEvents normalizes listed_venue_name in events. The events table has
// no unique constraint on listed_venue_name, so no duplicate-key handling is needed.
func normalizeEvents(ctx context.Context, tx pgx.Tx, logger *logging.Logger) (int, error) {
	rows, err := tx.Query(ctx, `SELECT id, listed_venue_name FROM events WHERE listed_venue_name IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("query events: %w", err)
	}

	type row struct {
		id   string
		name string
	}
	var toUpdate []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan events row: %w", err)
		}
		normalized := entity.NormalizeVenueName(r.name)
		if normalized != r.name {
			toUpdate = append(toUpdate, row{id: r.id, name: normalized})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate events rows: %w", err)
	}

	for _, r := range toUpdate {
		if _, err := tx.Exec(ctx, `UPDATE events SET listed_venue_name = $1 WHERE id = $2`, r.name, r.id); err != nil {
			return 0, fmt.Errorf("update events row %s: %w", r.id, err)
		}
	}

	logger.Info(ctx, "events normalization prepared", slog.Int("to_update", len(toUpdate)))
	return len(toUpdate), nil
}
