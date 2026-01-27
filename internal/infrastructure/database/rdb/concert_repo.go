package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// ConcertRepository implements entity.ConcertRepository interface for PostgreSQL.
type ConcertRepository struct {
	db *Database
}

const (
	listConcertsByArtistQuery = `
		SELECT
			id, artist_id, venue_id, title, date, start_time, open_time, status, created_at, updated_at
		FROM concerts
		WHERE artist_id = $1
	`
	insertConcertQuery = `
		INSERT INTO concerts (
			id, artist_id, venue_id, title, date, start_time, open_time, status, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
	`
)

// NewConcertRepository creates a new concert repository instance.
func NewConcertRepository(db *Database) *ConcertRepository {
	return &ConcertRepository{db: db}
}

// ListByArtist retrieves all concerts for a specific artist.
func (r *ConcertRepository) ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	rows, err := r.db.Pool.Query(ctx, listConcertsByArtistQuery, artistID)
	if err != nil {
		return nil, toAppErr(err, "failed to list concerts by artist", slog.String("artist_id", artistID))
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		var c entity.Concert
		err := rows.Scan(
			&c.ID, &c.ArtistID, &c.VenueID, &c.Title, &c.Date, &c.StartTime, &c.OpenTime, &c.Status, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, toAppErr(err, "failed to scan concert")
		}
		concerts = append(concerts, &c)
	}
	return concerts, nil
}

// Create creates a new concert in the database.
func (r *ConcertRepository) Create(ctx context.Context, concert *entity.Concert) error {
	_, err := r.db.Pool.Exec(ctx, insertConcertQuery,
		concert.ID, concert.ArtistID, concert.VenueID, concert.Title, concert.Date, concert.StartTime, concert.OpenTime, concert.Status,
	)
	if err != nil {
		return toAppErr(err, "failed to create concert", slog.String("concert_id", concert.ID), slog.String("artist_id", concert.ArtistID))
	}
	return nil
}
