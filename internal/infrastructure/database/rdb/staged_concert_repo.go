package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// StagedConcertRepository implements entity.StagedConcertRepository for
// PostgreSQL.
type StagedConcertRepository struct {
	db *Database
}

const (
	// upsertStagedConcertByPlaceQuery handles the resolved-venue path: when
	// ResolvedPlaceID is set the natural key is
	// (artist_id, local_date, resolved_place_id). The ON CONFLICT target
	// mirrors the partial index uq_staged_concerts_by_place. On conflict the
	// mutable payload is refreshed but discovered_at is kept so queue order is
	// stable.
	upsertStagedConcertByPlaceQuery = `
		INSERT INTO staged_concerts (
			id, artist_id, title, local_date, start_at, open_at,
			listed_venue_name, admin_area, source_url,
			resolved_place_id, resolved_venue_name, resolved_admin_area,
			resolved_latitude, resolved_longitude
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (artist_id, local_date, resolved_place_id)
		WHERE resolved_place_id IS NOT NULL
		DO UPDATE SET
			title               = EXCLUDED.title,
			start_at            = EXCLUDED.start_at,
			open_at             = EXCLUDED.open_at,
			admin_area          = EXCLUDED.admin_area,
			source_url          = EXCLUDED.source_url,
			resolved_venue_name = EXCLUDED.resolved_venue_name,
			resolved_admin_area = EXCLUDED.resolved_admin_area,
			resolved_latitude   = EXCLUDED.resolved_latitude,
			resolved_longitude  = EXCLUDED.resolved_longitude
	`

	// upsertStagedConcertByListedNameQuery handles the unresolved-venue path:
	// when ResolvedPlaceID is nil the natural key is
	// (artist_id, local_date, listed_venue_name). The ON CONFLICT target
	// mirrors the partial index uq_staged_concerts_by_listed_name. On conflict
	// the mutable payload is refreshed but discovered_at is kept.
	upsertStagedConcertByListedNameQuery = `
		INSERT INTO staged_concerts (
			id, artist_id, title, local_date, start_at, open_at,
			listed_venue_name, admin_area, source_url,
			resolved_place_id, resolved_venue_name, resolved_admin_area,
			resolved_latitude, resolved_longitude
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (artist_id, local_date, listed_venue_name)
		WHERE resolved_place_id IS NULL
		DO UPDATE SET
			title               = EXCLUDED.title,
			start_at            = EXCLUDED.start_at,
			open_at             = EXCLUDED.open_at,
			admin_area          = EXCLUDED.admin_area,
			source_url          = EXCLUDED.source_url,
			resolved_place_id   = EXCLUDED.resolved_place_id,
			resolved_venue_name = EXCLUDED.resolved_venue_name,
			resolved_admin_area = EXCLUDED.resolved_admin_area,
			resolved_latitude   = EXCLUDED.resolved_latitude,
			resolved_longitude  = EXCLUDED.resolved_longitude
	`

	listPendingStagedConcertsQuery = `
		SELECT id, artist_id, title, local_date, start_at, open_at,
		       listed_venue_name, admin_area, source_url,
		       resolved_place_id, resolved_venue_name, resolved_admin_area,
		       resolved_latitude, resolved_longitude, discovered_at
		FROM staged_concerts
		ORDER BY discovered_at ASC
	`

	getStagedConcertByIDQuery = `
		SELECT id, artist_id, title, local_date, start_at, open_at,
		       listed_venue_name, admin_area, source_url,
		       resolved_place_id, resolved_venue_name, resolved_admin_area,
		       resolved_latitude, resolved_longitude, discovered_at
		FROM staged_concerts
		WHERE id = $1
	`

	deleteStagedConcertQuery = `
		DELETE FROM staged_concerts WHERE id = $1
	`

	listPendingDedupKeysByArtistQuery = `
		SELECT local_date, listed_venue_name
		FROM staged_concerts
		WHERE artist_id = $1
	`
)

// NewStagedConcertRepository creates a new StagedConcertRepository instance.
func NewStagedConcertRepository(db *Database) *StagedConcertRepository {
	return &StagedConcertRepository{db: db}
}

// Upsert inserts a new staged concert or refreshes an existing pending row on
// natural-key conflict.
func (r *StagedConcertRepository) Upsert(ctx context.Context, sc *entity.StagedConcert) error {
	// Branch on whether ResolvedPlaceID is set to choose the correct partial
	// unique index as the ON CONFLICT target.
	var query string
	if sc.ResolvedPlaceID != nil {
		query = upsertStagedConcertByPlaceQuery
	} else {
		query = upsertStagedConcertByListedNameQuery
	}

	_, err := r.db.Pool.Exec(ctx, query,
		sc.ID,
		sc.ArtistID,
		sc.Title,
		sc.LocalDate,
		sc.StartTime,
		sc.OpenTime,
		sc.ListedVenueName,
		sc.AdminArea,
		sc.SourceURL,
		sc.ResolvedPlaceID,
		sc.ResolvedVenueName,
		sc.ResolvedAdminArea,
		sc.ResolvedLatitude,
		sc.ResolvedLongitude,
	)
	if err != nil {
		return toAppErr(err, "failed to upsert staged concert",
			slog.String("staged_concert_id", sc.ID),
			slog.String("artist_id", sc.ArtistID),
		)
	}
	return nil
}

// ListPending returns all pending staged concerts ordered by discovered_at ASC.
func (r *StagedConcertRepository) ListPending(ctx context.Context) ([]*entity.StagedConcert, error) {
	rows, err := r.db.Pool.Query(ctx, listPendingStagedConcertsQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list pending staged concerts")
	}
	defer rows.Close()

	var result []*entity.StagedConcert
	for rows.Next() {
		sc, err := scanStagedConcertRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "staged concert row iteration ended with error")
	}
	return result, nil
}

// GetByID returns the staged concert with the given ID.
func (r *StagedConcertRepository) GetByID(ctx context.Context, id string) (*entity.StagedConcert, error) {
	row := r.db.Pool.QueryRow(ctx, getStagedConcertByIDQuery, id)
	sc, err := scanStagedConcertRow(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get staged concert by ID",
			slog.String("staged_concert_id", id),
		)
	}
	return sc, nil
}

// Delete removes the staged concert with the given ID. It is idempotent.
func (r *StagedConcertRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.Pool.Exec(ctx, deleteStagedConcertQuery, id)
	if err != nil {
		return toAppErr(err, "failed to delete staged concert",
			slog.String("staged_concert_id", id),
		)
	}
	return nil
}

// ListPendingDedupKeysByArtist returns the (local_date, listed_venue_name)
// pairs for all pending rows belonging to the given artist.
func (r *StagedConcertRepository) ListPendingDedupKeysByArtist(ctx context.Context, artistID string) ([]entity.StagedConcertDedupKey, error) {
	rows, err := r.db.Pool.Query(ctx, listPendingDedupKeysByArtistQuery, artistID)
	if err != nil {
		return nil, toAppErr(err, "failed to list pending dedup keys for artist",
			slog.String("artist_id", artistID),
		)
	}
	defer rows.Close()

	var keys []entity.StagedConcertDedupKey
	for rows.Next() {
		var (
			localDate       time.Time
			listedVenueName string
		)
		if err := rows.Scan(&localDate, &listedVenueName); err != nil {
			return nil, toAppErr(err, "failed to scan staged concert dedup key")
		}
		keys = append(keys, entity.StagedConcertDedupKey{
			LocalDate:       localDate,
			ListedVenueName: listedVenueName,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "staged concert dedup key iteration ended with error")
	}
	return keys, nil
}

// scanStagedConcertRow scans a single row from the standard staged_concerts
// SELECT into a StagedConcert entity.
func scanStagedConcertRow(scan func(dest ...any) error) (*entity.StagedConcert, error) {
	var sc entity.StagedConcert
	err := scan(
		&sc.ID,
		&sc.ArtistID,
		&sc.Title,
		&sc.LocalDate,
		&sc.StartTime,
		&sc.OpenTime,
		&sc.ListedVenueName,
		&sc.AdminArea,
		&sc.SourceURL,
		&sc.ResolvedPlaceID,
		&sc.ResolvedVenueName,
		&sc.ResolvedAdminArea,
		&sc.ResolvedLatitude,
		&sc.ResolvedLongitude,
		&sc.DiscoveredTime,
	)
	if err != nil {
		return nil, err
	}
	return &sc, nil
}
