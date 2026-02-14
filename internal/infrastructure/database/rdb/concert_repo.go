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
		SELECT c.event_id, c.artist_id, e.venue_id, e.title, e.local_event_date, e.start_at, e.open_at, e.source_url
		FROM concerts c
		JOIN events e ON c.event_id = e.id
		WHERE c.artist_id = $1
	`
	listUpcomingConcertsByArtistQuery = `
		SELECT c.event_id, c.artist_id, e.venue_id, e.title, e.local_event_date, e.start_at, e.open_at, e.source_url
		FROM concerts c
		JOIN events e ON c.event_id = e.id
		WHERE c.artist_id = $1 AND e.local_event_date >= CURRENT_DATE
	`
	insertEventQuery = `
		INSERT INTO events (id, venue_id, title, local_event_date, start_at, open_at, source_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	insertConcertQuery = `
		INSERT INTO concerts (event_id, artist_id)
		VALUES ($1, $2)
	`
)

// NewConcertRepository creates a new concert repository instance.
func NewConcertRepository(db *Database) *ConcertRepository {
	return &ConcertRepository{db: db}
}

// ListByArtist retrieves concerts for a specific artist, optionally filtering for upcoming ones.
func (r *ConcertRepository) ListByArtist(ctx context.Context, artistID string, upcomingOnly bool) ([]*entity.Concert, error) {
	query := listConcertsByArtistQuery
	if upcomingOnly {
		query = listUpcomingConcertsByArtistQuery
	}

	rows, err := r.db.Pool.Query(ctx, query, artistID)
	if err != nil {
		return nil, toAppErr(err, "failed to list concerts by artist", slog.String("artist_id", artistID))
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		var c entity.Concert
		err := rows.Scan(&c.ID, &c.ArtistID, &c.VenueID, &c.Title, &c.LocalEventDate, &c.StartTime, &c.OpenTime, &c.SourceURL)
		if err != nil {
			return nil, toAppErr(err, "failed to scan concert")
		}
		concerts = append(concerts, &c)
	}
	return concerts, nil
}

// Create creates a new concert in the database.
func (r *ConcertRepository) Create(ctx context.Context, concert *entity.Concert) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, insertEventQuery,
		concert.ID, concert.VenueID, concert.Title, concert.LocalEventDate, concert.StartTime, concert.OpenTime, concert.SourceURL,
	)
	if err != nil {
		return toAppErr(err, "failed to create event", slog.String("event_id", concert.ID))
	}

	_, err = tx.Exec(ctx, insertConcertQuery, concert.ID, concert.ArtistID)
	if err != nil {
		return toAppErr(err, "failed to create concert", slog.String("concert_id", concert.ID), slog.String("artist_id", concert.ArtistID))
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit transaction")
	}

	return nil
}
