package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// TicketSaleSearchRepository implements [entity.TicketSaleSearchRepository]
// for PostgreSQL.
type TicketSaleSearchRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.TicketSaleSearchRepository = (*TicketSaleSearchRepository)(nil)

// NewTicketSaleSearchRepository creates a new TicketSaleSearchRepository.
func NewTicketSaleSearchRepository(db *Database) *TicketSaleSearchRepository {
	return &TicketSaleSearchRepository{db: db}
}

const (
	listTicketSaleSearchsBySeriesQuery = `
		SELECT series_id, searched_at
		FROM ticket_sale_searches
		WHERE series_id = ANY($1::uuid[])
	`

	// recordTicketSaleSearchsQuery sets searched_at for every given series
	// in one statement, so a foreign-key violation on any series rolls back
	// the whole batch.
	recordTicketSaleSearchsQuery = `
		INSERT INTO ticket_sale_searches (series_id, searched_at)
		SELECT series_id, $2 FROM unnest($1::uuid[]) AS t(series_id)
		ON CONFLICT (series_id) DO UPDATE SET searched_at = EXCLUDED.searched_at
	`
)

// ListBySeries returns the search of each given series that has one.
func (r *TicketSaleSearchRepository) ListBySeries(ctx context.Context, seriesIDs []string) ([]*entity.TicketSaleSearch, error) {
	if len(seriesIDs) == 0 {
		return []*entity.TicketSaleSearch{}, nil
	}

	rows, err := r.db.Pool.Query(ctx, listTicketSaleSearchsBySeriesQuery, seriesIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to list ticket sale searches", slog.Int("series_count", len(seriesIDs)))
	}
	defer rows.Close()

	searches := make([]*entity.TicketSaleSearch, 0, len(seriesIDs))
	for rows.Next() {
		var l entity.TicketSaleSearch
		if err := rows.Scan(&l.SeriesID, &l.SearchedTime); err != nil {
			return nil, toAppErr(err, "failed to scan ticket sale search")
		}
		searches = append(searches, &l)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "ticket sale search iteration ended with error")
	}
	return searches, nil
}

// Record sets the searched time of every given series to searchedTime.
func (r *TicketSaleSearchRepository) Record(ctx context.Context, seriesIDs []string, searchedTime time.Time) error {
	if len(seriesIDs) == 0 {
		return nil
	}
	search := &entity.TicketSaleSearch{SearchedTime: searchedTime}
	if err := search.Validate(time.Now()); err != nil {
		return apperr.New(codes.InvalidArgument, err.Error())
	}

	if _, err := r.db.Pool.Exec(ctx, recordTicketSaleSearchsQuery, seriesIDs, searchedTime); err != nil {
		return toAppErr(err, "failed to record ticket sale searches", slog.Int("series_count", len(seriesIDs)))
	}
	return nil
}
