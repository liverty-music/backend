package rdb

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Compile-time interface compliance check.
var _ usecase.EventPublishStatePort = (*EventPublishStateRepository)(nil)

// EventPublishStateRepository implements [usecase.EventPublishStatePort] for
// PostgreSQL. It answers whether a given event's parent series is PUBLISHED,
// distinguishing "event not found" from "event exists but not published".
type EventPublishStateRepository struct {
	db *Database
}

// NewEventPublishStateRepository creates a new EventPublishStateRepository.
func NewEventPublishStateRepository(db *Database) *EventPublishStateRepository {
	return &EventPublishStateRepository{db: db}
}

const (
	// eventExistsQuery checks whether an event row with the given ID exists.
	eventExistsQuery = `SELECT EXISTS(SELECT 1 FROM events WHERE id = $1)`

	// eventPublishedQuery returns true only when the event exists AND its parent
	// series has publish_state = 'PUBLISHED'. Discovered series carry NULL
	// publish_state, so COALESCE maps NULL to FALSE (not published).
	eventPublishedQuery = `
		SELECT EXISTS(
			SELECT 1
			FROM events e
			JOIN series s ON e.series_id = s.id
			WHERE e.id = $1
			  AND COALESCE(s.publish_state::text, '') = 'PUBLISHED'
		)
	`
)

// IsEventPublished implements [usecase.EventPublishStatePort].
//
// It returns:
//   - (true,  nil):  the event exists and its series is PUBLISHED.
//   - (false, nil):  the event exists but the series is DRAFT, CANCELLED, or discovered (NULL).
//   - (false, err):  err carries NotFound when the event does not exist, or
//     Internal for a database failure.
func (r *EventPublishStateRepository) IsEventPublished(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, apperr.New(codes.InvalidArgument, "event_id must not be empty")
	}

	// First check whether the event row itself exists so we can return a
	// meaningful NotFound rather than a silent false.
	var exists bool
	err := r.db.Pool.QueryRow(ctx, eventExistsQuery, eventID).Scan(&exists)
	if err != nil {
		// pgx.ErrNoRows should not occur for an EXISTS query, but handle it
		// defensively in the same way as other unexpected failures.
		if err == pgx.ErrNoRows {
			return false, apperr.New(codes.NotFound, "event not found")
		}
		return false, toAppErr(err, "failed to check event existence",
			slog.String("event_id", eventID),
		)
	}
	if !exists {
		return false, apperr.New(codes.NotFound, "event not found")
	}

	// Event exists — now check whether its series is PUBLISHED.
	var published bool
	err = r.db.Pool.QueryRow(ctx, eventPublishedQuery, eventID).Scan(&published)
	if err != nil {
		return false, toAppErr(err, "failed to check event publish state",
			slog.String("event_id", eventID),
		)
	}
	return published, nil
}
