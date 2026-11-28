package rdb

import (
	"context"
	"log/slog"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

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

// listRejectedConcertsByArtistQuery selects the listing fields of an
// Artist's entries, oldest first.
const listRejectedConcertsByArtistQuery = `
	SELECT id, artist_id, local_date, listed_venue_name, local_start_time::text
	FROM rejected_concerts
	WHERE artist_id = $1
	ORDER BY id
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

// ListByArtist returns the listing fields of the Artist's entries, oldest
// first.
func (r *RejectedConcertRepository) ListByArtist(ctx context.Context, artistID string) ([]*entity.RejectedConcert, error) {
	if artistID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist id must not be empty")
	}
	rows, err := r.db.Pool.Query(ctx, listRejectedConcertsByArtistQuery, artistID)
	if err != nil {
		return nil, toAppErr(err, "failed to list rejected concerts of artist", slog.String("artist_id", artistID))
	}
	defer rows.Close()

	var entries []*entity.RejectedConcert
	for rows.Next() {
		var (
			e         entity.RejectedConcert
			startText *string
		)
		if err := rows.Scan(&e.ID, &e.ArtistID, &e.LocalDate, &e.ListedVenueName, &startText); err != nil {
			return nil, toAppErr(err, "failed to scan rejected concert", slog.String("artist_id", artistID))
		}
		e.StartTime = timeOfDayFromText(startText)
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "rejected concert iteration ended with error", slog.String("artist_id", artistID))
	}
	return entries, nil
}
