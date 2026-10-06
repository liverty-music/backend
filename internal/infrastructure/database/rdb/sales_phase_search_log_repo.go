package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// SalesPhaseSearchLogRepository implements [entity.SalesPhaseSearchLogRepository]
// for PostgreSQL.
type SalesPhaseSearchLogRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.SalesPhaseSearchLogRepository = (*SalesPhaseSearchLogRepository)(nil)

// NewSalesPhaseSearchLogRepository creates a new SalesPhaseSearchLogRepository.
func NewSalesPhaseSearchLogRepository(db *Database) *SalesPhaseSearchLogRepository {
	return &SalesPhaseSearchLogRepository{db: db}
}

const (
	listSalesPhaseSearchLogsBySeriesQuery = `
		SELECT series_id, searched_at
		FROM sales_phase_search_logs
		WHERE series_id = ANY($1::uuid[])
	`

	// recordSalesPhaseSearchLogsQuery sets searched_at for every given series
	// in one statement, so a foreign-key violation on any series rolls back
	// the whole batch.
	recordSalesPhaseSearchLogsQuery = `
		INSERT INTO sales_phase_search_logs (series_id, searched_at)
		SELECT series_id, $2 FROM unnest($1::uuid[]) AS t(series_id)
		ON CONFLICT (series_id) DO UPDATE SET searched_at = EXCLUDED.searched_at
	`
)

// ListBySeries returns the log of each given series that has one.
func (r *SalesPhaseSearchLogRepository) ListBySeries(ctx context.Context, seriesIDs []string) ([]*entity.SalesPhaseSearchLog, error) {
	if len(seriesIDs) == 0 {
		return []*entity.SalesPhaseSearchLog{}, nil
	}

	rows, err := r.db.Pool.Query(ctx, listSalesPhaseSearchLogsBySeriesQuery, seriesIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to list sales phase search logs", slog.Int("series_count", len(seriesIDs)))
	}
	defer rows.Close()

	logs := make([]*entity.SalesPhaseSearchLog, 0, len(seriesIDs))
	for rows.Next() {
		var l entity.SalesPhaseSearchLog
		if err := rows.Scan(&l.SeriesID, &l.SearchedTime); err != nil {
			return nil, toAppErr(err, "failed to scan sales phase search log")
		}
		logs = append(logs, &l)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "sales phase search log iteration ended with error")
	}
	return logs, nil
}

// Record sets the searched time of every given series to searchedTime.
func (r *SalesPhaseSearchLogRepository) Record(ctx context.Context, seriesIDs []string, searchedTime time.Time) error {
	if len(seriesIDs) == 0 {
		return nil
	}
	log := &entity.SalesPhaseSearchLog{SearchedTime: searchedTime}
	if err := log.Validate(time.Now()); err != nil {
		return apperr.New(codes.InvalidArgument, err.Error())
	}

	if _, err := r.db.Pool.Exec(ctx, recordSalesPhaseSearchLogsQuery, seriesIDs, searchedTime); err != nil {
		return toAppErr(err, "failed to record sales phase search logs", slog.Int("series_count", len(seriesIDs)))
	}
	return nil
}
