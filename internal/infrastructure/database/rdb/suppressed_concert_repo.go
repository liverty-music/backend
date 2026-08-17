package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// SuppressedConcertRepository implements entity.SuppressedConcertRepository for
// PostgreSQL.
type SuppressedConcertRepository struct {
	db *Database
}

const (
	// insertSuppressedConcertQuery records a suppression entry. ON CONFLICT on the
	// NULLS NOT DISTINCT natural key makes a repeated suppression a no-op.
	insertSuppressedConcertQuery = `
		INSERT INTO suppressed_concerts (id, venue_id, local_event_date, start_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT ON CONSTRAINT uq_suppressed_concerts_natural_key DO NOTHING
	`

	// existsSuppressedConcertQuery matches the resolved natural key NULL-safely, so
	// a nil start time matches a suppressed unknown-start slot (NULLS NOT DISTINCT).
	existsSuppressedConcertQuery = `
		SELECT EXISTS(
			SELECT 1 FROM suppressed_concerts
			WHERE venue_id = $1 AND local_event_date = $2 AND start_at IS NOT DISTINCT FROM $3
		)
	`

	// deleteSuppressedConcertQuery removes a suppression entry (un-suppress). It
	// matches the natural key NULL-safely and is idempotent on an absent row.
	deleteSuppressedConcertQuery = `
		DELETE FROM suppressed_concerts
		WHERE venue_id = $1 AND local_event_date = $2 AND start_at IS NOT DISTINCT FROM $3
	`
)

// NewSuppressedConcertRepository creates a new SuppressedConcertRepository.
func NewSuppressedConcertRepository(db *Database) *SuppressedConcertRepository {
	return &SuppressedConcertRepository{db: db}
}

// Insert records a suppression entry, idempotent on the natural key.
func (r *SuppressedConcertRepository) Insert(ctx context.Context, sc *entity.SuppressedConcert) error {
	_, err := r.db.Pool.Exec(ctx, insertSuppressedConcertQuery,
		sc.ID,
		sc.VenueID,
		sc.LocalEventDate,
		sc.StartTime,
	)
	if err != nil {
		return toAppErr(err, "failed to insert suppressed concert",
			slog.String("venue_id", sc.VenueID),
		)
	}
	return nil
}

// Exists reports whether a suppression entry matches the given resolved natural
// key, matching start time NULL-safely.
func (r *SuppressedConcertRepository) Exists(ctx context.Context, venueID string, localDate time.Time, startTime *time.Time) (bool, error) {
	var exists bool
	if err := r.db.Pool.QueryRow(ctx, existsSuppressedConcertQuery, venueID, localDate, startTime).Scan(&exists); err != nil {
		return false, toAppErr(err, "failed to check suppressed concert",
			slog.String("venue_id", venueID),
		)
	}
	return exists, nil
}

// Delete removes the suppression entry for the given natural key (un-suppress),
// idempotent on an absent row.
func (r *SuppressedConcertRepository) Delete(ctx context.Context, venueID string, localDate time.Time, startTime *time.Time) error {
	if _, err := r.db.Pool.Exec(ctx, deleteSuppressedConcertQuery, venueID, localDate, startTime); err != nil {
		return toAppErr(err, "failed to delete suppressed concert",
			slog.String("venue_id", venueID),
		)
	}
	return nil
}
