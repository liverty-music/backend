package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// RejectedConcertLogRepository implements entity.RejectedConcertLogRepository
// for PostgreSQL.
type RejectedConcertLogRepository struct {
	db *Database
}

const insertRejectedConcertLogQuery = `
	INSERT INTO rejected_concerts_log (
		id, artist_id, artist_name, title, local_date, start_at, open_at,
		listed_venue_name, admin_area, source_url,
		resolved_place_id, resolved_venue_name, resolved_admin_area,
		reason, reviewed_by
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
`

// NewRejectedConcertLogRepository creates a new RejectedConcertLogRepository
// instance.
func NewRejectedConcertLogRepository(db *Database) *RejectedConcertLogRepository {
	return &RejectedConcertLogRepository{db: db}
}

// Append inserts a new rejection log entry.
func (r *RejectedConcertLogRepository) Append(ctx context.Context, log *entity.RejectedConcertLog) error {
	_, err := r.db.Pool.Exec(ctx, insertRejectedConcertLogQuery,
		log.ID,
		log.ArtistID,
		log.ArtistName,
		log.Title,
		log.LocalDate,
		log.StartTime,
		log.OpenTime,
		log.ListedVenueName,
		log.AdminArea,
		log.SourceURL,
		log.ResolvedPlaceID,
		log.ResolvedVenueName,
		log.ResolvedAdminArea,
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
