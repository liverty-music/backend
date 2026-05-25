package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// ConcertRepository implements entity.ConcertRepository interface for PostgreSQL.
type ConcertRepository struct {
	db *Database
}

const (
	// upsertEventsQuery bulk-inserts events with their new (series_id, local_event_date, venue_id)
	// natural key. On natural-key conflict the existing row is preserved and only NULL
	// open_at / start_at are filled in via COALESCE. The input id is discarded in that case;
	// callers detect this by re-querying with WHERE EXISTS on the input UUID.
	upsertEventsQuery = `
		INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date, start_at, open_at)
		SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::uuid[], $4::text[], $5::date[], $6::timestamptz[], $7::timestamptz[])
		ON CONFLICT ON CONSTRAINT uq_events_natural_key DO UPDATE SET
			start_at = COALESCE(events.start_at, EXCLUDED.start_at),
			open_at  = COALESCE(events.open_at, EXCLUDED.open_at)
	`

	// insertConcertsQuery inserts placeholder concerts rows only for events that
	// were genuinely inserted. The WHERE EXISTS check filters out input UUIDs
	// whose UPSERT lost a natural-key race.
	insertConcertsQuery = `
		INSERT INTO concerts (event_id)
		SELECT a.input_id
		FROM unnest($1::uuid[]) AS a(input_id)
		WHERE EXISTS (SELECT 1 FROM events e WHERE e.id = a.input_id)
		ON CONFLICT DO NOTHING
		RETURNING event_id
	`

	// insertEventPerformersQuery links events to their performing artists by
	// JOINing the events table on the natural key, so it picks up the actual
	// event row's UUID regardless of whether the caller's input UUID landed
	// in events (newly inserted) or was discarded in favour of a pre-existing
	// row (natural-key UPSERT conflict). This makes the M:N insert correct on
	// re-scrape / lineup-update — a second discovery that adds a new performer
	// to an already-known event still attaches the new performer to that
	// event's actual id. Idempotent via ON CONFLICT DO NOTHING.
	//
	// RETURNING event_id surfaces ONLY the genuinely new performer links
	// (re-deliveries hit ON CONFLICT and are not returned). Callers use this
	// to drive notification: an artist's followers must be notified whenever
	// the artist is newly attached to an event, whether the event itself is
	// brand-new OR pre-existing (the co-headliner case — artist B discovered
	// for an event artist A already created). Without RETURNING, the
	// pre-existing-event path silently skipped notifications because
	// insertConcertsQuery only RETURNs UUIDs that won the UPSERT race.
	insertEventPerformersQuery = `
		INSERT INTO event_performers (event_id, artist_id)
		SELECT e.id, perf.artist_id
		FROM unnest($1::uuid[], $2::date[], $3::uuid[], $4::uuid[])
			AS perf(series_id, local_event_date, venue_id, artist_id)
		JOIN events e
			ON e.series_id = perf.series_id
			AND e.local_event_date = perf.local_event_date
			AND e.venue_id = perf.venue_id
		ON CONFLICT DO NOTHING
		RETURNING event_id
	`

	// listConcertsByArtistQuery returns concerts where the given artist appears
	// in event_performers. The Series parent and the venue are joined; performer
	// hydration happens in a follow-up query (listPerformersByEventIDsQuery).
	listConcertsByArtistQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at,
		       s.title, s.type, s.source_url,
		       v.id, v.name, v.admin_area
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		WHERE EXISTS (
			SELECT 1 FROM event_performers ep WHERE ep.event_id = e.id AND ep.artist_id = $1
		)
		ORDER BY e.local_event_date ASC
	`

	listUpcomingConcertsByArtistQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at,
		       s.title, s.type, s.source_url,
		       v.id, v.name, v.admin_area
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		WHERE EXISTS (
			SELECT 1 FROM event_performers ep WHERE ep.event_id = e.id AND ep.artist_id = $1
		)
		AND e.local_event_date >= CURRENT_DATE
		ORDER BY e.local_event_date ASC
	`

	// listConcertsByArtistsQuery includes venue lat/lng for proximity classification.
	listConcertsByArtistsQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at,
		       s.title, s.type, s.source_url,
		       v.id, v.name, v.admin_area, v.latitude, v.longitude
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		WHERE EXISTS (
			SELECT 1 FROM event_performers ep WHERE ep.event_id = e.id AND ep.artist_id = ANY($1)
		)
		ORDER BY e.local_event_date ASC
	`

	// listConcertsByIDsQuery includes venue lat/lng because NotifyNewConcerts
	// feeds the result into HypeNearby.ShouldNotify, which calls ProximityTo
	// on Venue.Coordinates. Without the coordinates, ProximityTo returns
	// ProximityAway for every concert and HypeNearby followers are silently
	// excluded from every new-concert push notification.
	listConcertsByIDsQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at,
		       s.title, s.type, s.source_url,
		       v.id, v.name, v.admin_area, v.latitude, v.longitude
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		WHERE e.id = ANY($1)
		ORDER BY e.local_event_date ASC
	`

	// listConcertsByFollowerQuery joins followed_artists via event_performers.
	// Distinct is required because an event could have multiple performers that
	// are all followed by the same user; we want one row per event.
	listConcertsByFollowerQuery = `
		SELECT DISTINCT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at,
		       s.title, s.type, s.source_url,
		       v.id, v.name, v.admin_area, v.latitude, v.longitude
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		JOIN event_performers ep ON ep.event_id = e.id
		JOIN followed_artists fa ON fa.artist_id = ep.artist_id
		WHERE fa.user_id = $1
		ORDER BY e.local_event_date ASC
	`

	// listPerformersByEventIDsQuery hydrates the Performers slice on each Concert.
	// One row per (event_id, artist) pair so callers can group in Go.
	// ORDER BY a.id keeps the per-event performer order stable across queries so
	// callers that assert on order (handler tests, UI snapshots) don't flake on
	// PostgreSQL plan changes. The artist id is deterministic at insert time,
	// which produces a consistent — if not semantically "billed" — ordering;
	// promoting a billing/role column when needed is tracked separately.
	listPerformersByEventIDsQuery = `
		SELECT ep.event_id, a.id, a.name, a.mbid
		FROM event_performers ep
		JOIN artists a ON a.id = ep.artist_id
		WHERE ep.event_id = ANY($1)
		ORDER BY ep.event_id, a.id
	`
)

// NewConcertRepository creates a new concert repository instance.
func NewConcertRepository(db *Database) *ConcertRepository {
	return &ConcertRepository{db: db}
}

// scanConcertRow scans a row from the standard JOIN (events + series + venue)
// into a Concert without populating Performers. Pass withCoords=true when the
// query selects venue lat/lng (used by ListByArtists / ListByFollower).
func scanConcertRow(rowScan func(dest ...any) error, withCoords bool) (*entity.Concert, error) {
	var (
		c         entity.Concert
		series    entity.Series
		venue     entity.Venue
		seriesT   string
		sourceURL *string
		lat, lng  *float64
	)
	dests := []any{
		&c.ID, &c.SeriesID, &c.VenueID, &c.ListedVenueName, &c.LocalDate, &c.StartTime, &c.OpenTime,
		&series.Title, &seriesT, &sourceURL,
		&venue.ID, &venue.Name, &venue.AdminArea,
	}
	if withCoords {
		dests = append(dests, &lat, &lng)
	}
	if err := rowScan(dests...); err != nil {
		return nil, err
	}
	series.ID = c.SeriesID
	// Validate the DB-side series_type against the Go-side allowlist. Without
	// this check, an enum value added to the Postgres `series_type` type
	// before the Go binary is updated (e.g. a future `RESIDENCY`) would
	// silently cast to entity.SeriesType("RESIDENCY"), then collapse to
	// SERIES_TYPE_UNSPECIFIED at the proto mapper's default branch. The
	// Connect server only validates inbound requests, so the bad value would
	// reach the client as a structurally-valid-but-typeless concert. Failing
	// fast here surfaces the version skew before the response is built.
	switch entity.SeriesType(seriesT) {
	case entity.SeriesTypeTour, entity.SeriesTypeSingle, entity.SeriesTypeFestival:
		series.Type = entity.SeriesType(seriesT)
	default:
		return nil, apperr.New(codes.Internal,
			"unknown series_type from DB — Go binary may be behind a Postgres enum extension",
			slog.String("series_id", c.SeriesID),
			slog.String("series_type", seriesT),
		)
	}
	if sourceURL != nil {
		series.SourceURL = *sourceURL
	}
	if lat != nil && lng != nil {
		venue.Coordinates = &entity.Coordinates{Latitude: *lat, Longitude: *lng}
	}
	c.Series = &series
	c.Venue = &venue
	return &c, nil
}

// hydratePerformers fetches event_performers + artists for the given concerts
// and assigns each Concert.Performers slice. Concerts with no performers are
// left with a nil slice; callers downstream are expected to treat that as a
// data anomaly because every Event MUST have at least one performer.
func (r *ConcertRepository) hydratePerformers(ctx context.Context, concerts []*entity.Concert) error {
	if len(concerts) == 0 {
		return nil
	}

	ids := make([]string, len(concerts))
	byID := make(map[string]*entity.Concert, len(concerts))
	for i, c := range concerts {
		ids[i] = c.ID
		byID[c.ID] = c
	}

	rows, err := r.db.Pool.Query(ctx, listPerformersByEventIDsQuery, ids)
	if err != nil {
		return toAppErr(err, "failed to list performers for events", slog.Int("count", len(ids)))
	}
	defer rows.Close()

	for rows.Next() {
		var (
			eventID string
			artist  entity.Artist
		)
		if err := rows.Scan(&eventID, &artist.ID, &artist.Name, &artist.MBID); err != nil {
			return toAppErr(err, "failed to scan performer")
		}
		c, ok := byID[eventID]
		if !ok {
			continue
		}
		a := artist
		c.Performers = append(c.Performers, &a)
	}
	if err := rows.Err(); err != nil {
		return toAppErr(err, "performer iteration ended with error")
	}
	return nil
}

// ListByArtist retrieves concerts where the given artist is one of the performers.
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
		c, err := scanConcertRow(rows.Scan, false)
		if err != nil {
			return nil, toAppErr(err, "failed to scan concert")
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}
	rows.Close()

	if err := r.hydratePerformers(ctx, concerts); err != nil {
		return nil, err
	}
	return concerts, nil
}

// ListByIDs retrieves concerts by their event IDs. Series, Venue, and Performers
// are all populated.
func (r *ConcertRepository) ListByIDs(ctx context.Context, ids []string) ([]*entity.Concert, error) {
	if len(ids) == 0 {
		return nil, apperr.New(codes.InvalidArgument, "concert IDs must not be empty")
	}

	rows, err := r.db.Pool.Query(ctx, listConcertsByIDsQuery, ids)
	if err != nil {
		return nil, toAppErr(err, "failed to list concerts by IDs")
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		// withCoords=true matches the lat/lng columns selected by
		// listConcertsByIDsQuery — required so NotifyNewConcerts can
		// evaluate HypeNearby followers via Venue.Coordinates.
		c, err := scanConcertRow(rows.Scan, true)
		if err != nil {
			return nil, toAppErr(err, "failed to scan concert")
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}
	rows.Close()

	if err := r.hydratePerformers(ctx, concerts); err != nil {
		return nil, err
	}
	return concerts, nil
}

// ListByFollower retrieves all concerts featuring artists the user follows.
// Venue lat/lng are included for proximity classification.
func (r *ConcertRepository) ListByFollower(ctx context.Context, userID string) ([]*entity.Concert, error) {
	rows, err := r.db.Pool.Query(ctx, listConcertsByFollowerQuery, userID)
	if err != nil {
		return nil, toAppErr(err, "failed to list concerts by follower", slog.String("user_id", userID))
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		c, err := scanConcertRow(rows.Scan, true)
		if err != nil {
			return nil, toAppErr(err, "failed to scan concert")
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}
	rows.Close()

	if err := r.hydratePerformers(ctx, concerts); err != nil {
		return nil, err
	}
	return concerts, nil
}

// ListByArtists retrieves concerts where any of the given artists is a performer.
// Venue lat/lng are included for proximity classification.
func (r *ConcertRepository) ListByArtists(ctx context.Context, artistIDs []string) ([]*entity.Concert, error) {
	rows, err := r.db.Pool.Query(ctx, listConcertsByArtistsQuery, artistIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to list concerts by artists")
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		c, err := scanConcertRow(rows.Scan, true)
		if err != nil {
			return nil, toAppErr(err, "failed to scan concert")
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}
	rows.Close()

	if err := r.hydratePerformers(ctx, concerts); err != nil {
		return nil, err
	}
	return concerts, nil
}

// Create persists one or more concerts using bulk insert with UPSERT semantics.
//
// Caller MUST have already created the parent Series rows via
// [SeriesRepository.Create]; this method only inserts into events, concerts, and
// event_performers. Each concert MUST carry a non-empty SeriesID matching one of
// those Series rows (FK enforced).
//
// Events use UPSERT on (series_id, local_event_date, venue_id). On conflict the
// pre-existing event keeps its id and only NULL start/open times are filled.
// The placeholder concerts row and the event_performers links are only inserted
// for events whose input UUID survived the UPSERT.
//
// Returns the event IDs of concerts that were genuinely inserted (i.e., not
// deduplicated by natural-key UPSERT).
func (r *ConcertRepository) Create(ctx context.Context, concerts ...*entity.Concert) ([]string, error) {
	if len(concerts) == 0 {
		return nil, nil
	}

	// Compact the slice first.
	var valid []*entity.Concert
	for _, c := range concerts {
		if c != nil {
			valid = append(valid, c)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}

	n := len(valid)
	eventIDs := make([]string, n)
	seriesIDs := make([]string, n)
	venueIDs := make([]string, n)
	listedVenueNames := make([]*string, n)
	eventDates := make([]time.Time, n)
	startTimes := make([]*time.Time, n)
	openTimes := make([]*time.Time, n)

	// Flatten (series_id, local_event_date, venue_id, artist_id) tuples from
	// each concert's Performers slice. The natural-key triple lets the
	// insertEventPerformersQuery JOIN onto the actual event row regardless of
	// whether our input event UUID landed or lost the UPSERT race — this is
	// what makes re-scrape lineup updates correctly attach to the existing
	// event id instead of being silently dropped.
	var (
		performerSeriesIDs  []string
		performerEventDates []time.Time
		performerVenueIDs   []string
		performerArtistIDs  []string
	)

	for i, c := range valid {
		if c.SeriesID == "" {
			return nil, apperr.New(codes.InvalidArgument, "concert must carry a SeriesID before insert")
		}
		if len(c.Performers) == 0 {
			return nil, apperr.New(codes.InvalidArgument, "concert must have at least one performer before insert")
		}
		eventIDs[i] = c.ID
		seriesIDs[i] = c.SeriesID
		venueIDs[i] = c.VenueID
		listedVenueNames[i] = c.ListedVenueName
		eventDates[i] = c.LocalDate
		startTimes[i] = c.StartTime
		openTimes[i] = c.OpenTime
		for _, p := range c.Performers {
			if p == nil || p.ID == "" {
				return nil, apperr.New(codes.InvalidArgument, "performer ID must not be empty")
			}
			performerSeriesIDs = append(performerSeriesIDs, c.SeriesID)
			performerEventDates = append(performerEventDates, c.LocalDate)
			performerVenueIDs = append(performerVenueIDs, c.VenueID)
			performerArtistIDs = append(performerArtistIDs, p.ID)
		}
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, upsertEventsQuery,
		eventIDs, seriesIDs, venueIDs, listedVenueNames, eventDates, startTimes, openTimes,
	); err != nil {
		return nil, toAppErr(err, "failed to upsert events", slog.Int("count", n))
	}

	rows, err := tx.Query(ctx, insertConcertsQuery, eventIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to insert concerts", slog.Int("count", n))
	}
	defer rows.Close()
	insertedIDs := make([]string, 0, n)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, toAppErr(err, "failed to scan inserted concert id")
		}
		insertedIDs = append(insertedIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert insert RETURNING iteration ended with error",
			slog.Int("count", n),
		)
	}
	rows.Close()

	// linkedEventIDs collects the event IDs returned by
	// insertEventPerformersQuery — these are the events where one of THIS
	// batch's performer links was genuinely new (ON CONFLICT DO NOTHING
	// excludes re-deliveries from RETURNING). The union with insertedIDs
	// covers the co-headliner notification case: when artist B is
	// discovered for an event artist A already created, the events UPSERT
	// keeps the existing row, insertConcertsQuery returns nothing — but
	// the event_performers RETURNING surfaces the new (event, B) link so
	// B's followers get notified.
	var linkedEventIDs []string
	if len(performerArtistIDs) > 0 {
		linkRows, err := tx.Query(ctx, insertEventPerformersQuery,
			performerSeriesIDs, performerEventDates, performerVenueIDs, performerArtistIDs,
		)
		if err != nil {
			return nil, toAppErr(err, "failed to insert event_performers",
				slog.Int("event_count", n),
				slog.Int("link_count", len(performerArtistIDs)),
			)
		}
		defer linkRows.Close()
		for linkRows.Next() {
			var id string
			if err := linkRows.Scan(&id); err != nil {
				return nil, toAppErr(err, "failed to scan linked event id")
			}
			linkedEventIDs = append(linkedEventIDs, id)
		}
		if err := linkRows.Err(); err != nil {
			return nil, toAppErr(err, "event_performers insert RETURNING iteration ended with error",
				slog.Int("link_count", len(performerArtistIDs)),
			)
		}
		linkRows.Close()
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit transaction")
	}

	// Union insertedIDs with linkedEventIDs and dedup. A brand-new event
	// appears in both sets (insertConcertsQuery returned its UUID, AND the
	// performer link RETURNING surfaced it). A co-headliner-on-existing-
	// event case appears only in linkedEventIDs. A pure re-delivery (event
	// AND link both pre-exist) appears in neither, which is correct — no
	// new notification work to do.
	notifiableIDs := make([]string, 0, len(insertedIDs)+len(linkedEventIDs))
	seen := make(map[string]bool, len(insertedIDs)+len(linkedEventIDs))
	for _, id := range insertedIDs {
		if !seen[id] {
			seen[id] = true
			notifiableIDs = append(notifiableIDs, id)
		}
	}
	for _, id := range linkedEventIDs {
		if !seen[id] {
			seen[id] = true
			notifiableIDs = append(notifiableIDs, id)
		}
	}

	r.db.logger.Info(ctx, "concerts created",
		slog.String("entityType", "concert"),
		slog.Int("requested", n),
		slog.Int("inserted", len(insertedIDs)),
		slog.Int("newly_linked", len(linkedEventIDs)),
		slog.Int("notifiable", len(notifiableIDs)),
	)

	return notifiableIDs, nil
}
