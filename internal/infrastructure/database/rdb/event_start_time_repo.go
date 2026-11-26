package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
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
	SELECT e.local_event_date, e.local_start_time::text, v.time_zone
	FROM events e JOIN venues v ON v.id = e.venue_id
	WHERE e.id = $1
`

// GetEventStartTime implements [usecase.EventStartTimeRepository].
//
// Its StartTime is nil when local_start_time is NULL (event time not yet
// published).
func (r *EventStartTimeRepository) GetEventStartTime(ctx context.Context, eventID string) (*entity.EventStart, error) {
	var (
		start   entity.EventStart
		startAt *string
	)
	row := r.db.Pool.QueryRow(ctx, getEventStartTimeQuery, eventID)
	if err := row.Scan(&start.LocalDate, &startAt, &start.TimeZone); err != nil {
		return nil, toAppErr(err, "failed to get event start time",
			slog.String("event_id", eventID))
	}
	start.StartTime = timeOfDayFromText(startAt)
	return &start, nil
}
