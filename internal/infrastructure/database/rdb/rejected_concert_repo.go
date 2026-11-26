package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// RejectedConcertRepository implements entity.RejectedConcertRepository
// for PostgreSQL.
type RejectedConcertRepository struct {
	db *Database
}

const insertRejectedConcertQuery = `
	INSERT INTO rejected_concerts (
		id, artist_id, artist_name, title, local_date, local_start_time, local_open_time,
		listed_venue_name, admin_area, source_url,
		resolved_place_id, time_zone,
		reason, reviewed_by
	)
	VALUES ($1, $2, $3, $4, $5, $6::text::time, $7::text::time, $8, $9, $10, $11, $12, $13, $14)
`

// NewRejectedConcertRepository creates a new RejectedConcertRepository
// instance.
func NewRejectedConcertRepository(db *Database) *RejectedConcertRepository {
	return &RejectedConcertRepository{db: db}
}

// Append inserts a new rejection log entry.
func (r *RejectedConcertRepository) Append(ctx context.Context, log *entity.RejectedConcert) error {
	_, err := r.db.Pool.Exec(ctx, insertRejectedConcertQuery,
		log.ID,
		log.ArtistID,
		log.ArtistName,
		log.Title,
		log.LocalDate,
		timeOfDayText(log.StartTime),
		timeOfDayText(log.OpenTime),
		log.ListedVenueName,
		log.AdminArea,
		log.SourceURL,
		log.ResolvedPlaceID,
		log.TimeZone,
		log.Reason,
		log.ReviewedBy,
	)
	if err != nil {
		return toAppErr(err, "failed to append rejected concert log",
			slog.String("log_id", log.ID),
			slog.String("artist_id", log.ArtistID),
		)
	}
	return nil
}
