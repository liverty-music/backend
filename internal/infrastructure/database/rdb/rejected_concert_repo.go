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
		id, artist_id, artist_name, title, local_date, start_at, open_at,
		listed_venue_name, admin_area, source_url,
		resolved_place_id, resolved_venue_name, resolved_admin_area,
		reason, reviewed_by
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
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
