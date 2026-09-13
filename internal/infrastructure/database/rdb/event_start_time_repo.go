package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
)

// EventStartTimeRepository implements [usecase.EventStartTimeRepository] for
// PostgreSQL. It provides a minimal read-only interface over the events table
// so the settlement sweeper can check the release gate without depending on
// the full ConcertRepository.
type EventStartTimeRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ usecase.EventStartTimeRepository = (*EventStartTimeRepository)(nil)

// NewEventStartTimeRepository creates a new EventStartTimeRepository.
func NewEventStartTimeRepository(db *Database) *EventStartTimeRepository {
	return &EventStartTimeRepository{db: db}
}

const getEventStartTimeQuery = `
	SELECT start_at FROM events WHERE id = $1
`

// GetEventStartTime implements [usecase.EventStartTimeRepository].
//
// Returns nil when start_at is NULL (event time not yet published).
func (r *EventStartTimeRepository) GetEventStartTime(ctx context.Context, eventID string) (*time.Time, error) {
	var startAt sql.NullTime
	row := r.db.Pool.QueryRow(ctx, getEventStartTimeQuery, eventID)
	if err := row.Scan(&startAt); err != nil {
		return nil, toAppErr(err, "failed to get event start time",
			slog.String("event_id", eventID))
	}
	if !startAt.Valid {
		return nil, nil
	}
	t := startAt.Time
	return &t, nil
}
