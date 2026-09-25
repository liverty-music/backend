package rdb

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// EventOrganizerRepository implements [usecase.EventOrganizerRepository] for
// PostgreSQL. It provides a minimal read-only interface over events → series
// so the issuance path can stamp the denormalized OrganizerID on a Settlement
// without depending on the full SeriesRepository.
type EventOrganizerRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ usecase.EventOrganizerRepository = (*EventOrganizerRepository)(nil)

// NewEventOrganizerRepository creates a new EventOrganizerRepository.
func NewEventOrganizerRepository(db *Database) *EventOrganizerRepository {
	return &EventOrganizerRepository{db: db}
}

const getEventOrganizerIDQuery = `
	SELECT s.organizer_id
	FROM events e
	JOIN series s ON s.id = e.series_id
	WHERE e.id = $1
`

// GetOrganizerID implements [usecase.EventOrganizerRepository].
//
// Returns NotFound when the event does not exist or its series has no
// Organizer (a discovery-pipeline series never carries one).
func (r *EventOrganizerRepository) GetOrganizerID(ctx context.Context, eventID string) (string, error) {
	var organizerID sql.NullString
	row := r.db.Pool.QueryRow(ctx, getEventOrganizerIDQuery, eventID)
	if err := row.Scan(&organizerID); err != nil {
		return "", toAppErr(err, "failed to get event organizer", slog.String("event_id", eventID))
	}
	if !organizerID.Valid {
		return "", apperr.New(codes.NotFound,
			"event's series has no organizer (discovery-pipeline series)",
			slog.String("event_id", eventID))
	}
	return organizerID.String, nil
}
