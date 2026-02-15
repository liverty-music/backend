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
		SELECT artist_id, searched_at
		FROM latest_search_logs
		WHERE artist_id = $1
	`
	upsertSearchLogQuery = `
		INSERT INTO latest_search_logs (artist_id, searched_at)
		VALUES ($1, NOW())
		ON CONFLICT (artist_id) DO UPDATE SET searched_at = NOW()
	`
)

// NewSearchLogRepository creates a new search log repository instance.
func NewSearchLogRepository(db *Database) *SearchLogRepository {
	return &SearchLogRepository{db: db}
}

// GetByArtistID retrieves the search log for a specific artist.
func (r *SearchLogRepository) GetByArtistID(ctx context.Context, artistID string) (*entity.SearchLog, error) {
	var log entity.SearchLog
	err := r.db.Pool.QueryRow(ctx, getSearchLogByArtistIDQuery, artistID).Scan(&log.ArtistID, &log.SearchTime)
	if err != nil {
		return nil, toAppErr(err, "failed to get search log", slog.String("artist_id", artistID))
	}
	return &log, nil
}

// Upsert creates or updates the search log for an artist with the current timestamp.
func (r *SearchLogRepository) Upsert(ctx context.Context, artistID string) error {
	_, err := r.db.Pool.Exec(ctx, upsertSearchLogQuery, artistID)
	if err != nil {
		return toAppErr(err, "failed to upsert search log", slog.String("artist_id", artistID))
	}
	return nil
}
