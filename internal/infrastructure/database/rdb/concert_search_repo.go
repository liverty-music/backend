package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// ConcertSearchRepository implements entity.ConcertSearchRepository interface for PostgreSQL.
type ConcertSearchRepository struct {
	db *Database
}

const (
	getConcertSearchByArtistIDQuery = `
		SELECT artist_id, searched_at, status, last_found_at
		FROM concert_searches
		WHERE artist_id = $1
	`
	upsertConcertSearchQuery = `
		INSERT INTO concert_searches (artist_id, searched_at, status)
		VALUES ($1, NOW(), $2)
		ON CONFLICT (artist_id) DO UPDATE SET searched_at = NOW(), status = $2
	`
	updateConcertSearchStatusQuery = `
		UPDATE concert_searches
		SET status = $2
		WHERE artist_id = $1
	`
	markConcertSearchFoundQuery = `
		UPDATE concert_searches
		SET last_found_at = NOW()
		WHERE artist_id = $1
	`
	deleteConcertSearchQuery = `
		DELETE FROM concert_searches
		WHERE artist_id = $1
	`
)

// NewConcertSearchRepository creates a new concert search repository instance.
func NewConcertSearchRepository(db *Database) *ConcertSearchRepository {
	return &ConcertSearchRepository{db: db}
}

// GetByArtistID retrieves the concert search for a specific artist.
func (r *ConcertSearchRepository) GetByArtistID(ctx context.Context, artistID string) (*entity.ConcertSearch, error) {
	var log entity.ConcertSearch
	// last_found_at is nullable; scan into a pointer so a NULL maps to the
	// zero value rather than a scan error.
	var lastFound *time.Time
	err := r.db.Pool.QueryRow(ctx, getConcertSearchByArtistIDQuery, artistID).
		Scan(&log.ArtistID, &log.SearchTime, &log.Status, &lastFound)
	if err != nil {
		return nil, toAppErr(err, "failed to get concert search", slog.String("artist_id", artistID))
	}
	if lastFound != nil {
		log.LastFoundTime = *lastFound
	}
	return &log, nil
}

// Upsert creates or updates the concert search for an artist with the given status.
func (r *ConcertSearchRepository) Upsert(ctx context.Context, artistID string, status entity.ConcertSearchStatus) error {
	_, err := r.db.Pool.Exec(ctx, upsertConcertSearchQuery, artistID, string(status))
	if err != nil {
		return toAppErr(err, "failed to upsert concert search", slog.String("artist_id", artistID))
	}
	return nil
}

// UpdateStatus updates the status for an existing concert search.
func (r *ConcertSearchRepository) UpdateStatus(ctx context.Context, artistID string, status entity.ConcertSearchStatus) error {
	_, err := r.db.Pool.Exec(ctx, updateConcertSearchStatusQuery, artistID, string(status))
	if err != nil {
		return toAppErr(err, "failed to update concert search status", slog.String("artist_id", artistID))
	}
	return nil
}

// MarkFound records that a search for the artist discovered at least one new
// concert by setting last_found_at to the current time. It assumes a row
// already exists (the pending Upsert creates it before the search runs).
func (r *ConcertSearchRepository) MarkFound(ctx context.Context, artistID string) error {
	_, err := r.db.Pool.Exec(ctx, markConcertSearchFoundQuery, artistID)
	if err != nil {
		return toAppErr(err, "failed to mark concert search found", slog.String("artist_id", artistID))
	}
	return nil
}

// Delete removes the concert search for a specific artist.
func (r *ConcertSearchRepository) Delete(ctx context.Context, artistID string) error {
	_, err := r.db.Pool.Exec(ctx, deleteConcertSearchQuery, artistID)
	if err != nil {
		return toAppErr(err, "failed to delete concert search", slog.String("artist_id", artistID))
	}
	return nil
}
