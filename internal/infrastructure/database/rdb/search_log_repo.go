package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// SearchLogRepository implements entity.SearchLogRepository interface for PostgreSQL.
type SearchLogRepository struct {
	db *Database
}

const (
	getSearchLogByArtistIDQuery = `
		SELECT artist_id, searched_at, status
		FROM latest_search_logs
		WHERE artist_id = $1
	`
	listSearchLogsByArtistIDsQuery = `
		SELECT artist_id, searched_at, status
		FROM latest_search_logs
		WHERE artist_id = ANY($1)
	`
	upsertSearchLogQuery = `
		INSERT INTO latest_search_logs (artist_id, searched_at, status)
		VALUES ($1, NOW(), $2)
		ON CONFLICT (artist_id) DO UPDATE SET searched_at = NOW(), status = $2
	`
	updateSearchLogStatusQuery = `
		UPDATE latest_search_logs
		SET status = $2
		WHERE artist_id = $1
	`
	deleteSearchLogQuery = `
		DELETE FROM latest_search_logs
		WHERE artist_id = $1
	`
)

// NewSearchLogRepository creates a new search log repository instance.
func NewSearchLogRepository(db *Database) *SearchLogRepository {
	return &SearchLogRepository{db: db}
}

// GetByArtistID retrieves the search log for a specific artist.
func (r *SearchLogRepository) GetByArtistID(ctx context.Context, artistID string) (*entity.SearchLog, error) {
	var log entity.SearchLog
	err := r.db.Pool.QueryRow(ctx, getSearchLogByArtistIDQuery, artistID).
		Scan(&log.ArtistID, &log.SearchTime, &log.Status)
	if err != nil {
		return nil, toAppErr(err, "failed to get search log", slog.String("artist_id", artistID))
	}
	return &log, nil
}

// ListByArtistIDs retrieves search logs for multiple artists.
func (r *SearchLogRepository) ListByArtistIDs(ctx context.Context, artistIDs []string) ([]*entity.SearchLog, error) {
	rows, err := r.db.Pool.Query(ctx, listSearchLogsByArtistIDsQuery, artistIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to list search logs")
	}
	defer rows.Close()

	var logs []*entity.SearchLog
	for rows.Next() {
		var log entity.SearchLog
		if err := rows.Scan(&log.ArtistID, &log.SearchTime, &log.Status); err != nil {
			return nil, toAppErr(err, "failed to scan search log")
		}
		logs = append(logs, &log)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate search logs")
	}
	return logs, nil
}

// Upsert creates or updates the search log for an artist with the given status.
func (r *SearchLogRepository) Upsert(ctx context.Context, artistID string, status entity.SearchLogStatus) error {
	_, err := r.db.Pool.Exec(ctx, upsertSearchLogQuery, artistID, string(status))
	if err != nil {
		return toAppErr(err, "failed to upsert search log", slog.String("artist_id", artistID))
	}
	return nil
}

// UpdateStatus updates the status for an existing search log.
func (r *SearchLogRepository) UpdateStatus(ctx context.Context, artistID string, status entity.SearchLogStatus) error {
	_, err := r.db.Pool.Exec(ctx, updateSearchLogStatusQuery, artistID, string(status))
	if err != nil {
		return toAppErr(err, "failed to update search log status", slog.String("artist_id", artistID))
	}
	return nil
}

// Delete removes the search log for a specific artist.
func (r *SearchLogRepository) Delete(ctx context.Context, artistID string) error {
	_, err := r.db.Pool.Exec(ctx, deleteSearchLogQuery, artistID)
	if err != nil {
		return toAppErr(err, "failed to delete search log", slog.String("artist_id", artistID))
	}
	return nil
}
