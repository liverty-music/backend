package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// EventRescheduleTimeRepository implements [usecase.EventRescheduleTimeRepository]
// for PostgreSQL. It provides a minimal read-only interface over the events table
// so the refund use case can enforce the postponement holder-refund window without
// depending on the full ConcertRepository.
type EventRescheduleTimeRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ usecase.EventRescheduleTimeRepository = (*EventRescheduleTimeRepository)(nil)

// NewEventRescheduleTimeRepository creates a new EventRescheduleTimeRepository.
func NewEventRescheduleTimeRepository(db *Database) *EventRescheduleTimeRepository {
	return &EventRescheduleTimeRepository{db: db}
}

// getEventRescheduleTimeByOrderQuery returns the rescheduled_at timestamp for
// the event that the given order's tickets are issued for. The join goes
// orders → tickets → events. LIMIT 1 is safe because all tickets in an order
// reference the same event (one lottery phase → one event).
const getEventRescheduleTimeByOrderQuery = `
	SELECT e.rescheduled_at
	FROM tickets t
	JOIN events e ON e.id = t.event_id
	WHERE t.order_id = $1
	LIMIT 1
`

// GetRescheduleTimeByOrder implements [usecase.EventRescheduleTimeRepository].
//
// Returns nil when rescheduled_at is NULL (event not yet marked as rescheduled).
func (r *EventRescheduleTimeRepository) GetRescheduleTimeByOrder(ctx context.Context, orderID entity.OrderID) (*time.Time, error) {
	var rescheduledAt sql.NullTime
	row := r.db.Pool.QueryRow(ctx, getEventRescheduleTimeByOrderQuery, string(orderID))
	if err := row.Scan(&rescheduledAt); err != nil {
		return nil, toAppErr(err, "failed to get event reschedule time by order",
			slog.String("order_id", string(orderID)))
	}
	if !rescheduledAt.Valid {
		return nil, nil
	}
	t := rescheduledAt.Time
	return &t, nil
}
