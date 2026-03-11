package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// ConcertRepository implements entity.ConcertRepository interface for PostgreSQL.
type ConcertRepository struct {
	db *Database
}

const (
	upsertEventsQuery = `
		INSERT INTO events (id, venue_id, title, listed_venue_name, local_event_date, start_at, open_at, source_url)
		SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::text[], $5::date[], $6::timestamptz[], $7::timestamptz[], $8::text[])
		ON CONFLICT ON CONSTRAINT uq_events_natural_key DO UPDATE SET
			start_at = COALESCE(EXCLUDED.start_at, events.start_at),
			open_at  = COALESCE(EXCLUDED.open_at, events.open_at)
	`
	// insertConcertsQuery inserts concert rows only for events that were actually
	// inserted (not UPSERTed). The WHERE EXISTS check filters out input UUIDs that
	// don't exist in the events table — this happens when a natural-key conflict
	// caused the UPSERT to update an existing row whose id differs from the input.
	insertConcertsQuery = `
		INSERT INTO concerts (event_id, artist_id)
		SELECT a.input_id, a.artist_id
		FROM unnest($1::uuid[], $2::uuid[]) AS a(input_id, artist_id)
		WHERE EXISTS (SELECT 1 FROM events e WHERE e.id = a.input_id)
		ON CONFLICT DO NOTHING
	`
	listConcertsByArtistQuery = `
		SELECT c.event_id, c.artist_id, e.venue_id, e.title, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at, e.source_url,
		       v.id, v.name, v.admin_area
		FROM concerts c
		JOIN events e ON c.event_id = e.id
		JOIN venues v ON e.venue_id = v.id
		WHERE c.artist_id = $1
	`
	listUpcomingConcertsByArtistQuery = `
		SELECT c.event_id, c.artist_id, e.venue_id, e.title, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at, e.source_url,
		       v.id, v.name, v.admin_area
		FROM concerts c
		JOIN events e ON c.event_id = e.id
		JOIN venues v ON e.venue_id = v.id
		WHERE c.artist_id = $1 AND e.local_event_date >= CURRENT_DATE
	`
	listConcertsByFollowerQuery = `
		SELECT c.event_id, c.artist_id, e.venue_id, e.title, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at, e.source_url,
		       v.id, v.name, v.admin_area, v.latitude, v.longitude
		FROM concerts c
		JOIN events e ON c.event_id = e.id
		JOIN venues v ON e.venue_id = v.id
		JOIN followed_artists fa ON c.artist_id = fa.artist_id
		WHERE fa.user_id = $1
		ORDER BY e.local_event_date ASC
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
		var venue entity.Venue
		err := rows.Scan(
			&c.ID, &c.ArtistID, &c.VenueID, &c.Title, &c.ListedVenueName, &c.LocalDate, &c.StartTime, &c.OpenTime, &c.SourceURL,
			&venue.ID, &venue.Name, &venue.AdminArea,
		)
		if err != nil {
			return nil, toAppErr(err, "failed to scan concert")
		}
		c.Venue = &venue
		concerts = append(concerts, &c)
	}
	return concerts, nil
}

// ListByFollower retrieves all concerts for artists followed by the given user.
// Venue latitude and longitude are included for proximity classification.
func (r *ConcertRepository) ListByFollower(ctx context.Context, userID string) ([]*entity.Concert, error) {
	rows, err := r.db.Pool.Query(ctx, listConcertsByFollowerQuery, userID)
	if err != nil {
		return nil, toAppErr(err, "failed to list concerts by follower", slog.String("user_id", userID))
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		var c entity.Concert
		var venue entity.Venue
		var lat, lng *float64
		err := rows.Scan(
			&c.ID, &c.ArtistID, &c.VenueID, &c.Title, &c.ListedVenueName, &c.LocalDate, &c.StartTime, &c.OpenTime, &c.SourceURL,
			&venue.ID, &venue.Name, &venue.AdminArea, &lat, &lng,
		)
		if err != nil {
			return nil, toAppErr(err, "failed to scan concert")
		}
		if lat != nil && lng != nil {
			venue.Coordinates = &entity.Coordinates{Latitude: *lat, Longitude: *lng}
		}
		c.Venue = &venue
		concerts = append(concerts, &c)
	}
	return concerts, nil
}

// Create creates one or more concerts in the database within a single transaction.
// Uses PostgreSQL unnest for bulk inserts — no parameter limit, single statement per table.
//
// The events INSERT uses UPSERT on the natural key (venue_id, local_event_date, start_at).
// On conflict, only NULL time fields are filled via COALESCE — existing non-NULL values
// are preserved. The concerts INSERT uses WHERE EXISTS to skip rows whose input UUID
// was not inserted into events (i.e., the event already existed with a different id).
func (r *ConcertRepository) Create(ctx context.Context, concerts ...*entity.Concert) error {
	if len(concerts) == 0 {
		return nil
	}

	// Compact the slice first: skip nil elements so target arrays have no empty-value holes.
	// A nil element with index i would leave an empty string at eventIDs[i], which PostgreSQL
	// rejects as "invalid input syntax for type uuid: """.
	var valid []*entity.Concert
	for _, c := range concerts {
		if c != nil {
			valid = append(valid, c)
		}
	}
	if len(valid) == 0 {
		return nil
	}

	n := len(valid)
	eventIDs := make([]string, n)
	venueIDs := make([]string, n)
	titles := make([]string, n)
	listedVenueNames := make([]*string, n)
	eventDates := make([]time.Time, n)
	startTimes := make([]*time.Time, n)
	openTimes := make([]*time.Time, n)
	sourceURLs := make([]string, n)
	artistIDs := make([]string, n)

	for i, c := range valid {
		eventIDs[i] = c.ID
		venueIDs[i] = c.VenueID
		titles[i] = c.Title
		listedVenueNames[i] = c.ListedVenueName
		eventDates[i] = c.LocalDate
		startTimes[i] = c.StartTime
		openTimes[i] = c.OpenTime
		sourceURLs[i] = c.SourceURL
		artistIDs[i] = c.ArtistID
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, upsertEventsQuery, eventIDs, venueIDs, titles, listedVenueNames, eventDates, startTimes, openTimes, sourceURLs); err != nil {
		return toAppErr(err, "failed to upsert events", slog.Int("count", n))
	}

	if _, err := tx.Exec(ctx, insertConcertsQuery, eventIDs, artistIDs); err != nil {
		return toAppErr(err, "failed to insert concerts", slog.Int("count", n))
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit transaction")
	}

	r.db.logger.Info(ctx, "concerts created",
		slog.String("entityType", "concert"),
		slog.Int("count", n),
	)

	return nil
}
