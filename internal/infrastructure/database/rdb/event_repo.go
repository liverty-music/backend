package rdb

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// EventRepository implements [entity.EventRepository] for PostgreSQL: a
// read-only view of one event's current date, times and publish state.
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
	SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.local_start_time::text, e.local_open_time::text,
	       e.publish_state, e.per_account_limit, v.time_zone
	FROM events e JOIN venues v ON v.id = e.venue_id WHERE e.id = $1
`

// Get implements [entity.EventRepository].
func (r *EventRepository) Get(ctx context.Context, id string) (*entity.Event, error) {
	var (
		e          entity.Event
		listedName sql.NullString
		startAt    *string
		openAt     *string
		state      sql.NullString
		limit      sql.NullInt32
		zone       string
	)
	if err := r.db.Pool.QueryRow(ctx, getEventQuery, id).Scan(
		&e.ID, &e.SeriesID, &e.VenueID, &listedName, &e.LocalDate, &startAt, &openAt, &state, &limit, &zone,
	); err != nil {
		return nil, toAppErr(err, "failed to get event", slog.String("event_id", id))
	}
	if listedName.Valid {
		name := listedName.String
		e.ListedVenueName = &name
	}
	e.StartTime = timeOfDayFromText(startAt)
	e.OpenTime = timeOfDayFromText(openAt)
	// Only the Venue's time zone is read: the one thing an instant needs.
	e.Venue = &entity.Venue{ID: e.VenueID, TimeZone: &zone}
	e.PublishState = nullPublishState(state)
	e.PerAccountLimit = optionalInt(limit)
	return &e, nil
}
