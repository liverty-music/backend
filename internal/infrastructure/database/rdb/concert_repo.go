package rdb

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
)

// ConcertRepository implements entity.ConcertRepository interface for PostgreSQL.
type ConcertRepository struct {
	db *Database
}

const (
	// maxConcertsPerBatch limits the number of concerts inserted in a single SQL statement.
	// PostgreSQL has a maximum of 65,535 parameters per statement.
	// Each concert uses 7 parameters (events table) + 2 parameters (concerts table) = 9 total.
	// To stay well within limits and maintain readability, we batch at 500 concerts.
	maxConcertsPerBatch = 500

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

// Create creates one or more concerts in the database within a single transaction.
// For large batches, concerts are inserted in chunks to avoid PostgreSQL's parameter limit.
func (r *ConcertRepository) Create(ctx context.Context, concerts ...*entity.Concert) error {
	if len(concerts) == 0 {
		return nil
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Process concerts in batches to avoid PostgreSQL's 65,535 parameter limit
	for start := 0; start < len(concerts); start += maxConcertsPerBatch {
		end := start + maxConcertsPerBatch
		if end > len(concerts) {
			end = len(concerts)
		}
		batch := concerts[start:end]

		// Build bulk INSERT for events
		eventValues := make([]any, 0, len(batch)*7)
		eventPlaceholders := make([]string, 0, len(batch))

		for i, concert := range batch {
			offset := i * 7
			eventPlaceholders = append(eventPlaceholders,
				fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d)",
					offset+1, offset+2, offset+3, offset+4, offset+5, offset+6, offset+7))

			eventValues = append(eventValues,
				concert.ID, concert.VenueID, concert.Title,
				concert.LocalEventDate, concert.StartTime, concert.OpenTime, concert.SourceURL,
			)
		}

		bulkEventQuery := fmt.Sprintf(
			"INSERT INTO events (id, venue_id, title, local_event_date, start_at, open_at, source_url) VALUES %s ON CONFLICT DO NOTHING",
			strings.Join(eventPlaceholders, ", "),
		)

		if _, err := tx.Exec(ctx, bulkEventQuery, eventValues...); err != nil {
			return toAppErr(err, "failed to bulk insert events")
		}

		// Build bulk INSERT for concerts
		concertValues := make([]any, 0, len(batch)*2)
		concertPlaceholders := make([]string, 0, len(batch))

		for i, concert := range batch {
			offset := i * 2
			concertPlaceholders = append(concertPlaceholders,
				fmt.Sprintf("($%d, $%d)", offset+1, offset+2))
			concertValues = append(concertValues, concert.ID, concert.ArtistID)
		}

		bulkConcertQuery := fmt.Sprintf(
			"INSERT INTO concerts (event_id, artist_id) VALUES %s ON CONFLICT DO NOTHING",
			strings.Join(concertPlaceholders, ", "),
		)

		if _, err := tx.Exec(ctx, bulkConcertQuery, concertValues...); err != nil {
			return toAppErr(err, "failed to bulk insert concerts")
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit transaction")
	}

	return nil
}
