package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// ConcertCandidateRepository implements entity.ConcertCandidateRepository for
// PostgreSQL.
type ConcertCandidateRepository struct {
	db *Database
}

const (
	// upsertConcertCandidateByPlaceQuery handles the resolved-venue path: when
	// ResolvedPlaceID is set the natural key is
	// (artist_id, local_date, resolved_place_id). The ON CONFLICT target
	// mirrors the partial index uq_concert_candidates_by_place. On conflict the
	// mutable payload is refreshed but find_time is kept so queue order is
	// stable.
	upsertConcertCandidateByPlaceQuery = `
		INSERT INTO concert_candidates (
			id, artist_id, series_id, title, local_date, start_time, open_time,
			listed_venue_name, admin_area, source_url,
			resolved_place_id, time_zone
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (artist_id, local_date, resolved_place_id)
		WHERE resolved_place_id IS NOT NULL
		DO UPDATE SET
			series_id           = EXCLUDED.series_id,
			title               = EXCLUDED.title,
			start_time            = EXCLUDED.start_time,
			open_time             = EXCLUDED.open_time,
			admin_area          = EXCLUDED.admin_area,
			source_url          = EXCLUDED.source_url,
			time_zone           = EXCLUDED.time_zone
	`

	// upsertConcertCandidateByListedNameQuery handles the unresolved-venue path:
	// when ResolvedPlaceID is nil the natural key is
	// (artist_id, local_date, listed_venue_name). The ON CONFLICT target
	// mirrors the partial index uq_concert_candidates_by_listed_name. On conflict
	// the mutable payload is refreshed but find_time is kept.
	upsertConcertCandidateByListedNameQuery = `
		INSERT INTO concert_candidates (
			id, artist_id, series_id, title, local_date, start_time, open_time,
			listed_venue_name, admin_area, source_url,
			resolved_place_id, time_zone
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (artist_id, local_date, listed_venue_name)
		WHERE resolved_place_id IS NULL
		DO UPDATE SET
			series_id           = EXCLUDED.series_id,
			title               = EXCLUDED.title,
			start_time            = EXCLUDED.start_time,
			open_time             = EXCLUDED.open_time,
			admin_area          = EXCLUDED.admin_area,
			source_url          = EXCLUDED.source_url,
			resolved_place_id   = EXCLUDED.resolved_place_id,
			time_zone           = EXCLUDED.time_zone
	`

	listPendingConcertCandidatesQuery = `
		SELECT id, artist_id, series_id, title, local_date, start_time, open_time,
		       listed_venue_name, admin_area, source_url,
		       resolved_place_id, time_zone, find_time
		FROM concert_candidates
		ORDER BY find_time ASC
	`

	getConcertCandidateByIDQuery = `
		SELECT id, artist_id, series_id, title, local_date, start_time, open_time,
		       listed_venue_name, admin_area, source_url,
		       resolved_place_id, time_zone, find_time
		FROM concert_candidates
		WHERE id = $1
	`

	deleteConcertCandidateQuery = `
		DELETE FROM concert_candidates WHERE id = $1
	`

	listPendingDedupKeysByArtistQuery = `
		SELECT local_date, listed_venue_name
		FROM concert_candidates
		WHERE artist_id = $1
	`
)

// NewConcertCandidateRepository creates a new ConcertCandidateRepository instance.
func NewConcertCandidateRepository(db *Database) *ConcertCandidateRepository {
	return &ConcertCandidateRepository{db: db}
}

// Upsert inserts a new concert candidate or refreshes an existing pending row on
// natural-key conflict.
func (r *ConcertCandidateRepository) Upsert(ctx context.Context, sc *entity.ConcertCandidate) error {
	if err := sc.Validate(); err != nil {
		return err
	}
	// Branch on whether ResolvedPlaceID is set to choose the correct partial
	// unique index as the ON CONFLICT target.
	var query string
	if sc.ResolvedPlaceID != nil {
		query = upsertConcertCandidateByPlaceQuery
	} else {
		query = upsertConcertCandidateByListedNameQuery
	}

	_, err := r.db.Pool.Exec(ctx, query,
		sc.ID,
		sc.ArtistID,
		sc.SeriesID,
		sc.Title,
		sc.LocalDate,
		sc.StartTime,
		sc.OpenTime,
		sc.ListedVenueName,
		sc.AdminArea,
		sc.SourceURL,
		sc.ResolvedPlaceID,
		sc.TimeZone,
	)
	if err != nil {
		return toAppErr(err, "failed to upsert concert candidate",
			slog.String("concert_candidate_id", sc.ID),
			slog.String("artist_id", sc.ArtistID),
		)
	}
	return nil
}

// ListPending returns all pending concert candidates ordered by find_time ASC.
func (r *ConcertCandidateRepository) ListPending(ctx context.Context) ([]*entity.ConcertCandidate, error) {
	rows, err := r.db.Pool.Query(ctx, listPendingConcertCandidatesQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list pending concert candidates")
	}
	defer rows.Close()

	var result []*entity.ConcertCandidate
	for rows.Next() {
		sc, err := scanConcertCandidateRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert candidate row iteration ended with error")
	}
	return result, nil
}

// GetByID returns the concert candidate with the given ID.
func (r *ConcertCandidateRepository) GetByID(ctx context.Context, id string) (*entity.ConcertCandidate, error) {
	row := r.db.Pool.QueryRow(ctx, getConcertCandidateByIDQuery, id)
	sc, err := scanConcertCandidateRow(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get concert candidate by ID",
			slog.String("concert_candidate_id", id),
		)
	}
	return sc, nil
}

// Delete removes the concert candidate with the given ID. It is idempotent.
func (r *ConcertCandidateRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.Pool.Exec(ctx, deleteConcertCandidateQuery, id)
	if err != nil {
		return toAppErr(err, "failed to delete concert candidate",
			slog.String("concert_candidate_id", id),
		)
	}
	return nil
}

// ListPendingDedupKeysByArtist returns the (local_date, listed_venue_name)
// pairs for all pending rows belonging to the given artist.
func (r *ConcertCandidateRepository) ListPendingDedupKeysByArtist(ctx context.Context, artistID string) ([]entity.ConcertCandidateDedupKey, error) {
	rows, err := r.db.Pool.Query(ctx, listPendingDedupKeysByArtistQuery, artistID)
	if err != nil {
		return nil, toAppErr(err, "failed to list pending dedup keys for artist",
			slog.String("artist_id", artistID),
		)
	}
	defer rows.Close()

	var keys []entity.ConcertCandidateDedupKey
	for rows.Next() {
		var (
			localDate       time.Time
			listedVenueName string
		)
		if err := rows.Scan(&localDate, &listedVenueName); err != nil {
			return nil, toAppErr(err, "failed to scan concert candidate dedup key")
		}
		keys = append(keys, entity.ConcertCandidateDedupKey{
			LocalDate:       localDate,
			ListedVenueName: listedVenueName,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert candidate dedup key iteration ended with error")
	}
	return keys, nil
}

// scanConcertCandidateRow scans a single row from the standard concert_candidates
// SELECT into a ConcertCandidate entity.
func scanConcertCandidateRow(scan func(dest ...any) error) (*entity.ConcertCandidate, error) {
	var sc entity.ConcertCandidate
	err := scan(
		&sc.ID,
		&sc.ArtistID,
		&sc.SeriesID,
		&sc.Title,
		&sc.LocalDate,
		&sc.StartTime,
		&sc.OpenTime,
		&sc.ListedVenueName,
		&sc.AdminArea,
		&sc.SourceURL,
		&sc.ResolvedPlaceID,
		&sc.TimeZone,
		&sc.FindTime,
	)
	if err != nil {
		return nil, err
	}
	return &sc, nil
}
