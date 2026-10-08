package rdb

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// EventRepository implements [entity.EventRepository] for PostgreSQL: a
// read-only view of one event's current date and times.
type EventRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.EventRepository = (*EventRepository)(nil)

// NewEventRepository creates a new EventRepository.
func NewEventRepository(db *Database) *EventRepository {
	return &EventRepository{db: db}
}

const getEventQuery = `
	SELECT id, series_id, venue_id, listed_venue_name, local_event_date, start_at, open_at
	FROM events WHERE id = $1
`

// Get implements [entity.EventRepository].
func (r *EventRepository) Get(ctx context.Context, id string) (*entity.Event, error) {
	var (
		e          entity.Event
		listedName sql.NullString
		startAt    sql.NullTime
		openAt     sql.NullTime
	)
	if err := r.db.Pool.QueryRow(ctx, getEventQuery, id).Scan(
		&e.ID, &e.SeriesID, &e.VenueID, &listedName, &e.LocalDate, &startAt, &openAt,
	); err != nil {
		return nil, toAppErr(err, "failed to get event", slog.String("event_id", id))
	}
	if listedName.Valid {
		name := listedName.String
		e.ListedVenueName = &name
	}
	if startAt.Valid {
		t := startAt.Time
		e.StartTime = &t
	}
	if openAt.Valid {
		t := openAt.Time
		e.OpenTime = &t
	}
	return &e, nil
}
