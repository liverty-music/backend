package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// ConcertRepository implements entity.ConcertRepository interface for PostgreSQL.
type ConcertRepository struct {
	db     *Database
	outbox Announcer
}

const (
	// upsertEventsQuery bulk-inserts discovered events (publish_state NULL) on
	// the (venue_id, local_event_date, local_start_time) natural key, inferring the
	// partial unique index uq_events_natural_key, which skips DRAFT rows: a
	// discovered event never collides with, or fills the times of, a draft. On natural-key conflict the existing row is preserved and only NULL
	// local_open_time / local_start_time are filled in via COALESCE. The input id is discarded in that case;
	// callers detect this by re-querying with WHERE EXISTS on the input UUID.
	upsertEventsQuery = `
		INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date, local_start_time, local_open_time)
		SELECT u.id, u.series_id, u.venue_id, u.listed_venue_name, u.local_event_date, u.local_start_time::time, u.local_open_time::time
		FROM unnest($1::uuid[], $2::uuid[], $3::uuid[], $4::text[], $5::date[], $6::text[], $7::text[])
			AS u(id, series_id, venue_id, listed_venue_name, local_event_date, local_start_time, local_open_time)
		ON CONFLICT (venue_id, local_event_date, local_start_time) WHERE ` + notDraftPredicate + ` DO UPDATE SET
			local_start_time = COALESCE(events.local_start_time, EXCLUDED.local_start_time),
			local_open_time  = COALESCE(events.local_open_time, EXCLUDED.local_open_time)
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
		INSERT INTO concert_artists (event_id, artist_id)
		SELECT e.id, perf.artist_id
		FROM unnest($1::uuid[], $2::date[], $3::text[], $4::uuid[])
			AS perf(venue_id, local_event_date, start_time, artist_id)
		JOIN events e
			ON e.venue_id = perf.venue_id
			AND e.local_event_date = perf.local_event_date
			AND e.local_start_time IS NOT DISTINCT FROM perf.start_time::time
			AND ` + catalogEventCondition + `
		ON CONFLICT DO NOTHING
		RETURNING event_id, artist_id
	`

	// findEventsByVenueDateQuery returns existing events at any of the given
	// (venue_id, local_event_date) pairs, zipped element-wise from the two
	// array params, with their listed and other listed venue names, publish
	// state and performers. The pairs are deduplicated first so an event is
	// returned once.
	findEventsByVenueDateQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.other_listed_venue_names,
		       e.local_event_date, e.local_start_time::text, e.local_open_time::text, e.publish_state,
		       ` + eventPerformerIDsColumn + `
		FROM events e
		WHERE (e.venue_id, e.local_event_date) IN (
			SELECT pairs.venue_id, pairs.local_event_date
			FROM unnest($1::uuid[], $2::date[]) AS pairs(venue_id, local_event_date)
		)
		AND ` + catalogEventCondition + `
	`

	// findEventsByArtistDateQuery returns existing events on any of the given
	// dates at which the artist performs, with the same columns as
	// findEventsByVenueDateQuery.
	findEventsByArtistDateQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.other_listed_venue_names,
		       e.local_event_date, e.local_start_time::text, e.local_open_time::text, e.publish_state,
		       ` + eventPerformerIDsColumn + `
		FROM events e
		WHERE EXISTS (SELECT 1 FROM concert_artists ep WHERE ep.event_id = e.id AND ep.artist_id = $1)
			AND e.local_event_date = ANY($2::date[])
			AND ` + catalogEventCondition + `
	`

	// eventPerformerIDsColumn is the ids of the artists performing at the
	// event e, ordered by id.
	eventPerformerIDsColumn = `ARRAY(SELECT ca.artist_id::text FROM concert_artists ca WHERE ca.event_id = e.id ORDER BY ca.artist_id)`

	// listEventsBySeriesQuery returns every event of a series, ordered by
	// local_event_date ascending and, for equal dates, by local_start_time
	// ascending with a NULL local_start_time last (NULLS LAST). Projects only the physical-identity
	// fields a series-level notification link needs.
	listEventsBySeriesQuery = `
		SELECT e.id, e.series_id, e.local_event_date, e.local_start_time::text, e.publish_state
		FROM events e
		WHERE e.series_id = $1
		  AND ` + catalogEventCondition + `
		ORDER BY e.local_event_date ASC, e.local_start_time ASC NULLS LAST
	`

	// fillEventStartTimesQuery sets local_start_time / local_open_time on events
	// by id, only where currently NULL (COALESCE never overwrites a known time).
	// The three arrays are zipped element-wise.
	fillEventStartTimesQuery = `
		UPDATE events e
		SET local_start_time = COALESCE(e.local_start_time, u.start_time::time),
		    local_open_time  = COALESCE(e.local_open_time, u.open_time::time)
		FROM unnest($1::uuid[], $2::text[], $3::text[]) AS u(id, start_time, open_time)
		WHERE e.id = u.id
		  AND ` + catalogEventCondition + `
	`

	// fanVisibilityGuard leaves out every concert that is not visible to fans.
	// A concert is visible exactly when its event is discovered, or PUBLISHED
	// in a PUBLIC first-party series (components/entity/concert "Fan visibility
	// follows the event").
	fanVisibilityGuard = `
		AND ` + catalogEventCondition + `
		AND ` + fanVisibleCondition + `
	`

	// listConcertsByArtistQuery returns concerts where the given artist appears
	// in concert_artists. The Series parent and the venue are joined; performer
	// hydration happens in a follow-up query (listPerformersByEventIDsQuery).
	// The fanVisibilityGuard excludes DRAFT, CANCELLED and UNLISTED first-party
	// events from this fan-facing surface.
	listConcertsByArtistQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.local_start_time::text, e.local_open_time::text, e.publish_state,
		       s.title, s.type, s.source_url,
		       s.organizer_id, s.description, s.visibility, ` + seriesPublishStateColumn + `,
		       m.id, m.organizer_id,
		       v.id, v.name, v.admin_area, v.time_zone
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		WHERE EXISTS (
			SELECT 1 FROM concert_artists ep WHERE ep.event_id = e.id AND ep.artist_id = $1
		)` + fanVisibilityGuard + `
		ORDER BY e.local_event_date ASC
	`

	// listUpcomingConcertsByArtistQuery is listConcertsByArtistQuery from the
	// current date at each event's Venue onward: the day of the instant $2 in
	// the Venue's time zone ([entity.CurrentDateAtVenue]), never the
	// database's own date, whose day is the server's.
	listUpcomingConcertsByArtistQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.local_start_time::text, e.local_open_time::text, e.publish_state,
		       s.title, s.type, s.source_url,
		       s.organizer_id, s.description, s.visibility, ` + seriesPublishStateColumn + `,
		       m.id, m.organizer_id,
		       v.id, v.name, v.admin_area, v.time_zone
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		WHERE EXISTS (
			SELECT 1 FROM concert_artists ep WHERE ep.event_id = e.id AND ep.artist_id = $1
		)
		AND e.local_event_date >= (($2::timestamptz) AT TIME ZONE v.time_zone)::date - 1` + fanVisibilityGuard + `
		ORDER BY e.local_event_date ASC
	`

	// listConcertsByArtistsQuery includes venue lat/lng for proximity classification.
	// The fanVisibilityGuard excludes DRAFT, CANCELLED and UNLISTED first-party events.
	listConcertsByArtistsQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.local_start_time::text, e.local_open_time::text, e.publish_state,
		       s.title, s.type, s.source_url,
		       s.organizer_id, s.description, s.visibility, ` + seriesPublishStateColumn + `,
		       m.id, m.organizer_id,
		       v.id, v.name, v.admin_area, v.time_zone, v.latitude, v.longitude
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		WHERE EXISTS (
			SELECT 1 FROM concert_artists ep WHERE ep.event_id = e.id AND ep.artist_id = ANY($1)
		)` + fanVisibilityGuard + `
		ORDER BY e.local_event_date ASC
	`

	// listConcertsByLocationQuery returns candidate concerts in a date range whose
	// venue is a coarse spatial match for a reference point. The bounding box on
	// v.latitude / v.longitude is a cheap pre-filter that avoids a full-table scan;
	// the OR on v.admin_area guarantees HOME-tier venues are never missed even when
	// their coordinates fall outside the box (or are NULL). The precise Haversine
	// 200 km cut and HOME/NEARBY classification happen in the use-case layer.
	// Venue lat/lng are included (withCoords) so that classification can run.
	// Params: $1 from, $2 to, $3 latMin, $4 latMax, $5 lngMin, $6 lngMax,
	// $7 lngMin2, $8 lngMax2 (a second longitude range for a box across
	// ±180°, else the first one again), $9 admin_area. The box parameters are
	// NULL when the reference has no point.
	// The fanVisibilityGuard excludes DRAFT, CANCELLED and UNLISTED first-party events.
	listConcertsByLocationQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.local_start_time::text, e.local_open_time::text, e.publish_state,
		       s.title, s.type, s.source_url,
		       s.organizer_id, s.description, s.visibility, ` + seriesPublishStateColumn + `,
		       m.id, m.organizer_id,
		       v.id, v.name, v.admin_area, v.time_zone, v.latitude, v.longitude
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		WHERE e.local_event_date BETWEEN $1 AND $2
		  AND (
		    ($3::float8 IS NOT NULL AND v.latitude BETWEEN $3 AND $4
		     AND (v.longitude BETWEEN $5 AND $6 OR v.longitude BETWEEN $7 AND $8))
		    OR v.admin_area = $9
		  )` + fanVisibilityGuard + `
		ORDER BY e.local_event_date ASC
	`

	// catalogConcertsSelect is the projection and joins of the admin catalog
	// reads, scanned by scanConcertRow with coordinates.
	catalogConcertsSelect = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.local_start_time::text, e.local_open_time::text, e.publish_state,
		       s.title, s.type, s.source_url,
		       s.organizer_id, s.description, s.visibility, ` + seriesPublishStateColumn + `,
		       m.id, m.organizer_id,
		       v.id, v.name, v.admin_area, v.time_zone, v.latitude, v.longitude
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
	`

	// listAllConcertsQuery returns every published concert with no audience
	// filter, for the admin console's catalog management. Venue lat/lng are
	// included (withCoords) so the shared scanConcertRow path is reused.
	// Admin queries intentionally omit the visibility guard so admins can
	// see all series states.
	listAllConcertsQuery = catalogConcertsSelect + `
		WHERE ` + catalogEventCondition + `
		ORDER BY e.local_event_date ASC
	`

	// listCatalogByArtistQuery is listAllConcertsQuery narrowed to the
	// concerts the artist bound to $1 performs in.
	listCatalogByArtistQuery = catalogConcertsSelect + `
		WHERE ` + catalogEventCondition + `
		  AND EXISTS (SELECT 1 FROM concert_artists ca WHERE ca.event_id = e.id AND ca.artist_id = $1)
		ORDER BY e.local_event_date ASC
	`

	// catalogEventExistsQuery reports whether the id is a published (not
	// DRAFT) event; Delete reads it after lockAndCheckEventRemoval.
	catalogEventExistsQuery = `
		SELECT EXISTS (SELECT 1 FROM events e WHERE e.id = $1 AND ` + catalogEventCondition + `)
	`

	// listConcertsByIDsQuery includes venue lat/lng because NotifyNewConcerts
	// feeds the result into HypeNearby.MatchingConcerts, which calls ProximityTo
	// on Venue.Coordinates. Without the coordinates, ProximityTo returns
	// ProximityAway for every concert and HypeNearby followers are silently
	// excluded from every new-concert push notification.
	listConcertsByIDsQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.local_start_time::text, e.local_open_time::text, e.publish_state,
		       s.title, s.type, s.source_url,
		       s.organizer_id, s.description, s.visibility, ` + seriesPublishStateColumn + `,
		       m.id, m.organizer_id,
		       v.id, v.name, v.admin_area, v.time_zone, v.latitude, v.longitude
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		WHERE e.id = ANY($1)
		  AND ` + catalogEventCondition + `
		ORDER BY e.local_event_date ASC
	`

	// listConcertsByFollowerQuery joins followed_artists via concert_artists.
	// Distinct is required because an event could have multiple performers that
	// are all followed by the same user; we want one row per event.
	//
	// $2 is the lower bound of the local date: the caller's from, which may be
	// a past date; when it is NULL the lower bound is the current date at each
	// event's Venue, the day of the instant $3 in the Venue's time zone
	// ([entity.CurrentDateAtVenue]), never the database's own date, whose day
	// is the server's.
	//
	// The fanVisibilityGuard excludes DRAFT, CANCELLED and UNLISTED first-party
	// events from this fan-facing surface.
	listConcertsByFollowerQuery = `
		SELECT DISTINCT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.local_start_time::text, e.local_open_time::text, e.publish_state,
		       s.title, s.type, s.source_url,
		       s.organizer_id, s.description, s.visibility, ` + seriesPublishStateColumn + `,
		       m.id, m.organizer_id,
		       v.id, v.name, v.admin_area, v.time_zone, v.latitude, v.longitude
		FROM events e
		JOIN series s ON e.series_id = s.id
		JOIN venues v ON e.venue_id = v.id
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		JOIN concert_artists ep ON ep.event_id = e.id
		JOIN followed_artists fa ON fa.artist_id = ep.artist_id
		WHERE fa.user_id = $1
		  AND e.local_event_date >= COALESCE($2::date, (($3::timestamptz) AT TIME ZONE v.time_zone)::date - 1)` + fanVisibilityGuard + `
		ORDER BY e.local_event_date ASC
	`

	// listOrganizersByIDsQuery hydrates the Organizer of each first-party
	// Series, with the columns scanOrganizer reads.
	listOrganizersByIDsQuery = `
		SELECT id, name, short_name, owner_email, provider_organizer_id, status,
			legal_name, representative_name, business_address, business_phone_number, contact_email,
			platform_fee_rate_bps FROM organizers WHERE id = ANY($1)
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
		FROM concert_artists ep
		JOIN artists a ON a.id = ep.artist_id
		WHERE ep.event_id = ANY($1)
		ORDER BY ep.event_id, a.id
	`
)

// NewConcertRepository creates a new concert repository instance.
func NewConcertRepository(db *Database, outbox Announcer) *ConcertRepository {
	return &ConcertRepository{db: db, outbox: outbox}
}

// scanConcertRow scans a row from the standard JOIN (events + series + cover
// image + venue) into a Concert without populating Artists. Pass
// withCoords=true when the query selects venue lat/lng (used by ListByArtists /
// ListByFollower).
//
// The Series carries its first-party attributes (organizer, description,
// visibility, publish state) and its cover image's identity, so every read
// returns the same Series shape as the concert page. The share token is never
// read here.
func scanConcertRow(rowScan func(dest ...any) error, withCoords bool) (*entity.Concert, error) {
	var (
		c             entity.Concert
		series        entity.Series
		venue         entity.Venue
		seriesT       string
		sourceURL     *string
		eventState    sql.NullString
		organizerID   sql.NullString
		description   sql.NullString
		visibility    sql.NullString
		publishState  sql.NullString
		seriesImageID sql.NullString
		imageOrgID    sql.NullString
		lat, lng      *float64
		startText     *string
		openText      *string
	)
	dests := []any{
		&c.ID, &c.SeriesID, &c.VenueID, &c.ListedVenueName, &c.LocalDate, &startText, &openText, &eventState,
		&series.Title, &seriesT, &sourceURL,
		&organizerID, &description, &visibility, &publishState,
		&seriesImageID, &imageOrgID,
		&venue.ID, &venue.Name, &venue.AdminArea, &venue.TimeZone,
	}
	if withCoords {
		dests = append(dests, &lat, &lng)
	}
	if err := rowScan(dests...); err != nil {
		return nil, toAppErr(err, "failed to scan concert row")
	}
	c.StartTime = timeOfDayFromText(startText)
	c.OpenTime = timeOfDayFromText(openText)
	series.ID = c.SeriesID
	// Validate the DB-side series_type against the Go-side allowlist. Without
	// this check, an enum value added to the Postgres `series_type` type
	// before the Go binary is updated (e.g. a future `RESIDENCY`) would
	// silently cast to entity.SeriesType("RESIDENCY"), then collapse to
	// SERIES_TYPE_UNSPECIFIED at the proto mapper's default branch. The
	// Connect server only validates inbound requests, so the bad value would
	// reach the client as a structurally-valid-but-typeless concert. Failing
	// fast here surfaces the version skew before the response is built.
	switch {
	case entity.SeriesType(seriesT).IsValid():
		series.Type = entity.SeriesType(seriesT)
	default:
		return nil, apperr.New(codes.Internal,
			"unknown series_type from DB — Go binary may be behind a Postgres enum extension",
			slog.String("series_id", c.SeriesID),
			slog.String("series_type", seriesT),
		)
	}
	applySeriesColumns(&series, sourceURL, organizerID, description, visibility)
	c.PublishState = nullPublishState(eventState)
	series.PublishState = nullPublishState(publishState)
	if seriesImageID.Valid {
		series.CoverImage = &entity.SeriesImage{
			ID:          seriesImageID.String,
			OrganizerID: imageOrgID.String,
		}
	}
	if lat != nil && lng != nil {
		venue.Coordinates = &entity.Coordinates{Latitude: *lat, Longitude: *lng}
	}
	c.Series = &series
	c.Venue = &venue
	return &c, nil
}

// applySeriesColumns sets the Series attributes that are NULL in the database:
// a NULL column leaves the field unset.
func applySeriesColumns(series *entity.Series, sourceURL *string, organizerID, description, visibility sql.NullString) {
	if sourceURL != nil {
		series.SourceURL = *sourceURL
	}
	if organizerID.Valid {
		v := organizerID.String
		series.OrganizerID = &v
	}
	if description.Valid {
		v := description.String
		series.Description = &v
	}
	if visibility.Valid {
		v := entity.SeriesVisibility(visibility.String)
		series.Visibility = &v
	}
}

// hydrate fills each Concert's Performers and its first-party Series'
// Organizer.
func (r *ConcertRepository) hydrate(ctx context.Context, concerts []*entity.Concert) error {
	if err := r.hydratePerformers(ctx, concerts); err != nil {
		return err
	}
	return r.hydrateOrganizers(ctx, concerts)
}

// hydrateOrganizers reads the Organizers of the concerts' first-party Series in
// one query and sets each Series' Organizer.
func (r *ConcertRepository) hydrateOrganizers(ctx context.Context, concerts []*entity.Concert) error {
	var ids []string
	seen := make(map[string]bool)
	for _, c := range concerts {
		if c.Series == nil || c.Series.OrganizerID == nil || seen[*c.Series.OrganizerID] {
			continue
		}
		seen[*c.Series.OrganizerID] = true
		ids = append(ids, *c.Series.OrganizerID)
	}
	if len(ids) == 0 {
		return nil
	}

	rows, err := r.db.Pool.Query(ctx, listOrganizersByIDsQuery, ids)
	if err != nil {
		return toAppErr(err, "failed to list organizers of series", slog.Int("count", len(ids)))
	}
	defer rows.Close()

	byID := make(map[string]*entity.Organizer, len(ids))
	for rows.Next() {
		o, err := scanOrganizer(rows.Scan)
		if err != nil {
			return toAppErr(err, "failed to scan organizer")
		}
		byID[o.ID] = o
	}
	if err := rows.Err(); err != nil {
		return toAppErr(err, "organizer iteration ended with error")
	}
	for _, c := range concerts {
		if c.Series != nil && c.Series.OrganizerID != nil {
			c.Series.Organizer = byID[*c.Series.OrganizerID]
		}
	}
	return nil
}

// hydratePerformers fetches concert_artists + artists for the given concerts
// and assigns each Concert.Artists slice. Concerts with no performers are
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
		c.Artists = append(c.Artists, &a)
	}
	if err := rows.Err(); err != nil {
		return toAppErr(err, "performer iteration ended with error")
	}
	return nil
}

// ListByArtist retrieves the fan-visible concerts of every date at which the
// given artist is one of the performers.
func (r *ConcertRepository) ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	return r.listByArtist(ctx, artistID, listConcertsByArtistQuery, artistID)
}

// ListUpcomingByArtist retrieves the fan-visible concerts at which the given
// artist is one of the performers, from the current date at their Venue at
// now onward.
func (r *ConcertRepository) ListUpcomingByArtist(ctx context.Context, artistID string, now time.Time) ([]*entity.Concert, error) {
	concerts, err := r.listByArtist(ctx, artistID, listUpcomingConcertsByArtistQuery, artistID, now)
	if err != nil {
		return nil, err
	}
	return upcomingAt(concerts, now), nil
}

// upcomingAt keeps the concerts whose date has not passed at their Venue at
// now. The queries select from one day earlier, so a row is not lost while
// PostgreSQL's time zone data and the binary's differ; this applies the exact
// rule.
func upcomingAt(concerts []*entity.Concert, now time.Time) []*entity.Concert {
	return slices.DeleteFunc(concerts, func(c *entity.Concert) bool {
		return c.LocalDate.Before(entity.CurrentDateAtVenue(now, c.Venue))
	})
}

// listByArtist runs one of the by-artist concert queries and hydrates its
// rows.
func (r *ConcertRepository) listByArtist(ctx context.Context, artistID, query string, args ...any) ([]*entity.Concert, error) {
	if artistID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist id must not be empty")
	}

	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, toAppErr(err, "failed to list concerts by artist", slog.String("artist_id", artistID))
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		c, err := scanConcertRow(rows.Scan, false)
		if err != nil {
			// scanConcertRow already returns a classified error
			// (toAppErr-wrapped pgx errors or apperr.New for the
			// SeriesType allowlist failure); don't re-wrap here or the
			// outer fmt.Errorf would bury the structured slog attrs
			// and the codes.Internal designation downstream callers
			// rely on.
			return nil, err
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}

	if err := r.hydrate(ctx, concerts); err != nil {
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
			return nil, err
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}

	if err := r.hydrate(ctx, concerts); err != nil {
		return nil, err
	}
	return concerts, nil
}

// ListByFollower retrieves all concerts featuring artists the user follows whose
// local event date is on or after from. A nil from defaults to the current
// date at the Venue (today onward). Venue lat/lng are included for proximity
// classification.
func (r *ConcertRepository) ListByFollower(ctx context.Context, userID string, from *time.Time) ([]*entity.Concert, error) {
	now := time.Now()
	rows, err := r.db.Pool.Query(ctx, listConcertsByFollowerQuery, userID, from, now)
	if err != nil {
		return nil, toAppErr(err, "failed to list concerts by follower", slog.String("user_id", userID))
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		c, err := scanConcertRow(rows.Scan, true)
		if err != nil {
			return nil, err
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}

	if from == nil {
		concerts = upcomingAt(concerts, now)
	}

	if err := r.hydrate(ctx, concerts); err != nil {
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
			return nil, err
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}

	if err := r.hydrate(ctx, concerts); err != nil {
		return nil, err
	}
	return concerts, nil
}

// ListByLocation retrieves the concerts in [from, to] whose venue
// GeoLocation.MayBeNear admits: inside location.Box() (one or two longitude
// ranges) or in its admin area.
// Venue lat/lng are included so the use-case layer can apply the precise
// Haversine 200 km cut and HOME/NEARBY classification.
func (r *ConcertRepository) ListByLocation(ctx context.Context, location *entity.GeoLocation, from, to time.Time) ([]*entity.Concert, error) {
	var boxArgs [6]*float64
	if box, ok := location.Box(); ok {
		second := box.Lng[len(box.Lng)-1]
		boxArgs = [6]*float64{&box.LatMin, &box.LatMax, &box.Lng[0][0], &box.Lng[0][1], &second[0], &second[1]}
	}
	rows, err := r.db.Pool.Query(ctx, listConcertsByLocationQuery,
		from, to, boxArgs[0], boxArgs[1], boxArgs[2], boxArgs[3], boxArgs[4], boxArgs[5], location.AdminArea,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to list concerts by location",
			slog.String("admin_area", location.AdminArea),
		)
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		c, err := scanConcertRow(rows.Scan, true)
		if err != nil {
			return nil, err
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}

	if err := r.hydrate(ctx, concerts); err != nil {
		return nil, err
	}
	return concerts, nil
}

// List retrieves every published concert with no audience filter. Venue lat/lng
// are included for parity with the other proximity-aware list methods.
func (r *ConcertRepository) List(ctx context.Context) ([]*entity.Concert, error) {
	return r.listCatalog(ctx, "failed to list all concerts", listAllConcertsQuery)
}

// ListCatalogByArtist retrieves every published concert the artist performs
// in, with no audience filter. An artist without concerts, or an unknown id,
// yields an empty list.
func (r *ConcertRepository) ListCatalogByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	if artistID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist id must not be empty")
	}
	return r.listCatalog(ctx, "failed to list the artist's concerts", listCatalogByArtistQuery, artistID)
}

// listCatalog runs a catalog query and returns its concerts hydrated with their
// performers.
func (r *ConcertRepository) listCatalog(ctx context.Context, failure, query string, args ...any) ([]*entity.Concert, error) {
	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, toAppErr(err, failure)
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		c, err := scanConcertRow(rows.Scan, true)
		if err != nil {
			return nil, err
		}
		concerts = append(concerts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "concert row iteration ended with error")
	}

	if err := r.hydrate(ctx, concerts); err != nil {
		return nil, err
	}
	return concerts, nil
}

// Delete deletes a published event with everything that depends only on it
// (see removeEvents), in one transaction. The event's purchase records are
// locked and checked first (lockAndCheckEventRemoval); an id that is gone or
// DRAFT is then a no-op. Nothing is recorded about the event's slot.
//
// # Possible errors
//
//   - FailedPrecondition: the event has a Paid Order, a Settlement that is
//     not Reversed, or a purchase in progress.
func (r *ConcertRepository) Delete(ctx context.Context, eventID string) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin concert delete transaction", slog.String("event_id", eventID))
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	eventIDs := []string{eventID}
	if err := lockAndCheckEventRemoval(ctx, tx, eventIDs); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, catalogEventExistsQuery, eventID).Scan(&exists); err != nil {
		return toAppErr(err, "failed to read event", slog.String("event_id", eventID))
	}
	if !exists {
		return nil
	}
	if err := removeEvents(ctx, tx, eventIDs); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit concert delete", slog.String("event_id", eventID))
	}
	return nil
}

// Create persists one or more concerts using bulk insert with UPSERT semantics.
//
// Caller MUST have already created the parent Series rows via
// [SeriesRepository.Create]; this method only inserts into events, concerts, and
// concert_artists. Each concert MUST carry a non-empty SeriesID matching one of
// those Series rows (FK enforced).
//
// Events use UPSERT on (series_id, local_event_date, venue_id). On conflict the
// pre-existing event keeps its id and only NULL start/open times are filled.
// The placeholder concerts row and the concert_artists links are only inserted
// for events whose input UUID survived the UPSERT.
//
// Returns the event IDs of concerts that were genuinely inserted (i.e., not
// deduplicated by natural-key UPSERT).
//
// # Announces
//
// CONCERT.created once for each Artist newly linked to events of one Series
// by this call, carrying those Concerts (new events, and existing events the
// Artist was newly added to), with the Artist id as ce_subject, in the
// transaction of the change. Nothing when no link is new.
func (r *ConcertRepository) Create(ctx context.Context, concerts ...*entity.Concert) ([]string, error) {
	if len(concerts) == 0 {
		return nil, nil
	}

	// Compact the slice first AND dedup by the events physical natural key
	// `(venue_id, local_event_date, local_start_time)` (NULLS NOT DISTINCT — an unknown
	// start time keys as empty so two unpublished-time rows collapse). PostgreSQL
	// rejects `INSERT ... ON CONFLICT DO UPDATE` when two rows in the same
	// statement target the same conflict row with:
	//   ERROR: ON CONFLICT DO UPDATE command cannot affect row a second time
	// This can fire on the discovery path when Gemini returns two scraped
	// entries for the same physical venue under different ListedVenueNames
	// (e.g. "Zepp" + "Zepp DiverCity"): FilterNew dedups on raw ListedVenueName
	// so both survive, GetByListedName/Places resolves them to the same
	// venue_id, and with an identical date+start they collapse to one key →
	// batch UPSERT crash, the tx rolls back, and the Pub/Sub consumer retries
	// the message indefinitely. Today's only caller
	// (concert_creation_uc.CreateFromDiscovered) emits one batch per Pub/Sub
	// message and one message per artist, so every concert in a single Create
	// call shares the same artist set — dropping duplicates by natural key
	// cannot lose performer information. If a future caller batches concerts
	// from multiple artists, it must merge duplicates' Performers into the kept
	// entry before invoking Create; the Warn log surfaces if that happens.
	valid, dropped := entity.DedupByNaturalKey(concerts)
	for _, c := range dropped {
		r.db.logger.Warn(ctx, "Create: dropping duplicate concert with identical natural key from same batch",
			slog.String("concert_id", c.ID),
			slog.String("venue_id", c.VenueID),
			slog.String("local_date", c.LocalDate.Format("2006-01-02")),
			slog.String("start_time", entity.StartKey(c.StartTime)),
		)
	}
	if len(valid) == 0 {
		return nil, nil
	}

	batch, err := newConcertBatch(valid)
	if err != nil {
		return nil, err
	}
	n := len(valid)

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	insertedIDs, err := batch.insertEventsAndConcerts(ctx, tx)
	if err != nil {
		return nil, err
	}

	// linkedEventIDs are the events where one of THIS batch's performer links
	// was genuinely new (ON CONFLICT DO NOTHING excludes re-deliveries from
	// RETURNING). The union with insertedIDs covers the co-headliner
	// notification case: when artist B is discovered for an event artist A
	// already created, the events UPSERT keeps the existing row,
	// insertConcertsQuery returns nothing — but the concert_artists RETURNING
	// surfaces the new (event, B) link so B's followers get notified.
	// newlyListed maps each Artist to the events it was newly linked to.
	linkedEventIDs, newlyListed, err := batch.linkPerformers(ctx, tx)
	if err != nil {
		return nil, err
	}

	if err := announceNewConcerts(ctx, r.outbox, tx, newlyListed); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit transaction")
	}

	// A brand-new event is in both sets, a co-headliner on an existing event
	// only in linkedEventIDs, a pure re-delivery in neither.
	notifiableIDs := entity.NotifiableEventIDs(insertedIDs, linkedEventIDs)

	r.db.logger.Info(ctx, "concerts created",
		slog.String("entityType", "concert"),
		slog.Int("requested", n),
		slog.Int("inserted", len(insertedIDs)),
		slog.Int("newly_linked", len(linkedEventIDs)),
		slog.Int("notifiable", len(notifiableIDs)),
	)

	return notifiableIDs, nil
}

// concertBatch is the columns of the concerts a Create inserts, one entry per
// concert, and the (venue_id, local_event_date, start_time, artist_id) tuples of
// their performers.
type concertBatch struct {
	eventIDs         []string
	seriesIDs        []string
	venueIDs         []string
	listedVenueNames []*string
	eventDates       []time.Time
	startTimes       []*entity.TimeOfDay
	openTimes        []*entity.TimeOfDay

	// The performer tuples: the physical-key triple lets the
	// insertEventPerformersQuery JOIN onto the actual event row regardless of
	// whether our input event UUID landed or lost the UPSERT race — this is
	// what makes re-scrape lineup updates correctly attach to the existing
	// event id instead of being silently dropped. start_time is matched with
	// IS NOT DISTINCT FROM so it both disambiguates 昼夜2公演 and matches NULLs.
	performerVenueIDs   []string
	performerEventDates []time.Time
	performerStartAts   []*entity.TimeOfDay
	performerArtistIDs  []string
}

// newConcertBatch flattens the concerts into columns. It fails with
// InvalidArgument for a concert without an id, a venue, a series or a
// performer, or with a performer without an id.
func newConcertBatch(concerts []*entity.Concert) (*concertBatch, error) {
	n := len(concerts)
	b := &concertBatch{
		eventIDs:         make([]string, n),
		seriesIDs:        make([]string, n),
		venueIDs:         make([]string, n),
		listedVenueNames: make([]*string, n),
		eventDates:       make([]time.Time, n),
		startTimes:       make([]*entity.TimeOfDay, n),
		openTimes:        make([]*entity.TimeOfDay, n),
	}
	for i, c := range concerts {
		if c.ID == "" {
			return nil, apperr.New(codes.InvalidArgument, "concert must carry an ID (event UUID) before insert")
		}
		if c.VenueID == "" {
			return nil, apperr.New(codes.InvalidArgument, "concert must carry a VenueID before insert")
		}
		if c.SeriesID == "" {
			return nil, apperr.New(codes.InvalidArgument, "concert must carry a SeriesID before insert")
		}
		if len(c.Artists) == 0 {
			return nil, apperr.New(codes.InvalidArgument, "concert must have at least one performer before insert")
		}
		b.eventIDs[i] = c.ID
		b.seriesIDs[i] = c.SeriesID
		b.venueIDs[i] = c.VenueID
		b.listedVenueNames[i] = c.ListedVenueName
		b.eventDates[i] = c.LocalDate
		b.startTimes[i] = c.StartTime
		b.openTimes[i] = c.OpenTime
		for _, p := range c.Artists {
			if p == nil || p.ID == "" {
				return nil, apperr.New(codes.InvalidArgument, "performer ID must not be empty")
			}
			b.performerVenueIDs = append(b.performerVenueIDs, c.VenueID)
			b.performerEventDates = append(b.performerEventDates, c.LocalDate)
			b.performerStartAts = append(b.performerStartAts, c.StartTime)
			b.performerArtistIDs = append(b.performerArtistIDs, p.ID)
		}
	}
	return b, nil
}

// insertEventsAndConcerts upserts the events and inserts the placeholder
// concerts rows, and returns the ids of the concerts genuinely inserted.
func (b *concertBatch) insertEventsAndConcerts(ctx context.Context, tx pgx.Tx) ([]string, error) {
	n := len(b.eventIDs)
	if _, err := tx.Exec(ctx, upsertEventsQuery,
		b.eventIDs, b.seriesIDs, b.venueIDs, b.listedVenueNames, b.eventDates, timeOfDayTexts(b.startTimes), timeOfDayTexts(b.openTimes),
	); err != nil {
		return nil, toAppErr(err, "failed to upsert events", slog.Int("count", n))
	}

	rows, err := tx.Query(ctx, insertConcertsQuery, b.eventIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to insert concerts", slog.Int("count", n))
	}
	// pgx v5 forbids issuing a new query on the same transaction while a
	// previous Rows is still open, so the rows are closed before returning.
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
		return nil, toAppErr(err, "concert insert RETURNING iteration ended with error", slog.Int("count", n))
	}
	return insertedIDs, nil
}

// linkPerformers inserts the concert_artists links and returns the ids of the
// events one of whose links was new, and, per artist, the events it was newly
// linked to.
func (b *concertBatch) linkPerformers(ctx context.Context, tx pgx.Tx) ([]string, map[string][]string, error) {
	newlyListed := map[string][]string{}
	if len(b.performerArtistIDs) == 0 {
		return nil, newlyListed, nil
	}
	linkRows, err := tx.Query(ctx, insertEventPerformersQuery,
		b.performerVenueIDs, b.performerEventDates, timeOfDayTexts(b.performerStartAts), b.performerArtistIDs,
	)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to insert concert_artists",
			slog.Int("event_count", len(b.eventIDs)),
			slog.Int("link_count", len(b.performerArtistIDs)),
		)
	}
	defer linkRows.Close()
	var linkedEventIDs []string
	for linkRows.Next() {
		var id, artistID string
		if err := linkRows.Scan(&id, &artistID); err != nil {
			return nil, nil, toAppErr(err, "failed to scan linked event id")
		}
		linkedEventIDs = append(linkedEventIDs, id)
		newlyListed[artistID] = append(newlyListed[artistID], id)
	}
	if err := linkRows.Err(); err != nil {
		return nil, nil, toAppErr(err, "concert_artists insert RETURNING iteration ended with error",
			slog.Int("link_count", len(b.performerArtistIDs)),
		)
	}
	return linkedEventIDs, newlyListed, nil
}

// FindEventsByVenueAndDate implements entity.ConcertRepository. It returns
// the events at any of the supplied (venue_id, local_event_date) pairs with
// their names, times, publish state and performer ids.
func (r *ConcertRepository) FindEventsByVenueAndDate(ctx context.Context, venueIDs []string, dates []time.Time) ([]*entity.Concert, error) {
	if len(venueIDs) == 0 || len(dates) == 0 {
		return nil, nil
	}
	return r.findEvents(ctx, "venue and date", findEventsByVenueDateQuery, venueIDs, dates)
}

// FindEventsByArtistAndDate implements entity.ConcertRepository. It returns
// the events on any of the supplied local dates at which the artist
// performs, with the columns of [ConcertRepository.FindEventsByVenueAndDate].
func (r *ConcertRepository) FindEventsByArtistAndDate(ctx context.Context, artistID string, dates []time.Time) ([]*entity.Concert, error) {
	if artistID == "" || len(dates) == 0 {
		return nil, nil
	}
	return r.findEvents(ctx, "artist and date", findEventsByArtistDateQuery, artistID, dates)
}

// findEvents runs one of the find-events queries and scans its rows into
// Concerts whose Artists carry only their ids.
func (r *ConcertRepository) findEvents(ctx context.Context, by, query string, args ...any) ([]*entity.Concert, error) {
	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, toAppErr(err, "failed to find events by "+by)
	}
	defer rows.Close()

	var concerts []*entity.Concert
	for rows.Next() {
		var (
			c               entity.Concert
			startAt, openAt *string
			state           sql.NullString
			performerIDs    []string
		)
		if err := rows.Scan(&c.ID, &c.SeriesID, &c.VenueID, &c.ListedVenueName, &c.OtherListedVenueNames,
			&c.LocalDate, &startAt, &openAt, &state, &performerIDs); err != nil {
			return nil, toAppErr(err, "failed to scan event by "+by)
		}
		c.StartTime = timeOfDayFromText(startAt)
		c.OpenTime = timeOfDayFromText(openAt)
		c.PublishState = nullPublishState(state)
		for _, id := range performerIDs {
			c.Artists = append(c.Artists, &entity.Artist{ID: id})
		}
		concerts = append(concerts, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "events by "+by+" iteration ended with error")
	}
	return concerts, nil
}

// ListEventsBySeries implements entity.ConcertRepository. It returns every
// event of the given series that is not DRAFT, with its publish state, ordered by local_event_date then start_time
// (NULLS LAST), projected to the fields series-level notification link
// resolution needs.
func (r *ConcertRepository) ListEventsBySeries(ctx context.Context, seriesID string) ([]*entity.Event, error) {
	rows, err := r.db.Pool.Query(ctx, listEventsBySeriesQuery, seriesID)
	if err != nil {
		return nil, toAppErr(err, "failed to list events by series", slog.String("series_id", seriesID))
	}
	defer rows.Close()

	var events []*entity.Event
	for rows.Next() {
		var (
			e       entity.Event
			startAt *string
			state   sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.SeriesID, &e.LocalDate, &startAt, &state); err != nil {
			return nil, toAppErr(err, "failed to scan event by series")
		}
		e.StartTime = timeOfDayFromText(startAt)
		e.PublishState = nullPublishState(state)
		events = append(events, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "events by series iteration ended with error")
	}
	return events, nil
}

// FillEventStartTimes implements entity.ConcertRepository. It fills local_start_time /
// local_open_time on existing events by id, using COALESCE so a known time is never
// overwritten. It is one statement, so a fill that takes another event's slot
// violates uq_events_natural_key, fails with AlreadyExists and changes none of
// the events.
func (r *ConcertRepository) FillEventStartTimes(ctx context.Context, eventIDs []string, startTimes, openTimes []*entity.TimeOfDay) error {
	if len(eventIDs) == 0 {
		return nil
	}
	if _, err := r.db.Pool.Exec(ctx, fillEventStartTimesQuery, eventIDs, timeOfDayTexts(startTimes), timeOfDayTexts(openTimes)); err != nil {
		return toAppErr(err, "failed to fill event start times", slog.Int("count", len(eventIDs)))
	}
	return nil
}

// updateEventListedVenueNameQuery sets the event's listed venue name and keeps
// the name it replaces among its other listed venue names, unless that name is
// already there. The SET expressions read the row before the update. Giving
// the name the event already lists matches no row.
const updateEventListedVenueNameQuery = `
	UPDATE events e
	SET listed_venue_name = $2,
	    other_listed_venue_names = CASE
	        WHEN e.listed_venue_name IS NULL OR e.listed_venue_name = ANY(e.other_listed_venue_names)
	            THEN e.other_listed_venue_names
	        ELSE array_append(e.other_listed_venue_names, e.listed_venue_name)
	    END
	WHERE e.id = $1
	  AND e.listed_venue_name IS DISTINCT FROM $2
	  AND ` + catalogEventCondition

// UpdateEventListedVenueName implements entity.ConcertRepository. It replaces
// the listed_venue_name display field of an existing event, keeping the
// replaced name among its other listed venue names, and leaves venue_id and
// google_place_id untouched. A missing id updates zero rows and is a no-op.
func (r *ConcertRepository) UpdateEventListedVenueName(ctx context.Context, eventID string, listedVenueName string) error {
	if _, err := r.db.Pool.Exec(ctx, updateEventListedVenueNameQuery, eventID, listedVenueName); err != nil {
		return toAppErr(err, "failed to update event listed venue name", slog.String("event_id", eventID))
	}
	return nil
}

const (
	// lockEventNamesQuery reads and locks the names of an event that is not
	// DRAFT.
	lockEventNamesQuery = `
		SELECT e.listed_venue_name, e.other_listed_venue_names
		FROM events e
		WHERE e.id = $1 AND ` + catalogEventCondition + `
		FOR UPDATE
	`
	// setOtherListedVenueNamesQuery replaces the other listed venue names of
	// an event.
	setOtherListedVenueNamesQuery = `UPDATE events SET other_listed_venue_names = $2 WHERE id = $1`
)

// AddOtherListedVenueName implements entity.ConcertRepository. It reads the
// event under a row lock, asks [entity.Event.ListedUnder] whether the name is
// known, and appends it only when it is not, in one transaction: a plain
// array_append guarded in SQL would compare exact strings, not listed venues.
func (r *ConcertRepository) AddOtherListedVenueName(ctx context.Context, eventID, listedVenueName string) error {
	name := entity.NormalizeVenueName(listedVenueName)
	if name == "" {
		return apperr.New(codes.InvalidArgument, "listed venue name must name a venue", slog.String("event_id", eventID))
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin add other listed venue name transaction", slog.String("event_id", eventID))
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var e entity.Event
	if err := tx.QueryRow(ctx, lockEventNamesQuery, eventID).Scan(&e.ListedVenueName, &e.OtherListedVenueNames); err != nil {
		return toAppErr(err, "failed to read event names", slog.String("event_id", eventID))
	}
	if e.ListedUnder(name) {
		return nil
	}
	if _, err := tx.Exec(ctx, setOtherListedVenueNamesQuery, eventID, append(e.OtherListedVenueNames, name)); err != nil {
		return toAppErr(err, "failed to add other listed venue name", slog.String("event_id", eventID))
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit other listed venue name", slog.String("event_id", eventID))
	}
	return nil
}

const (
	// lockMergedEventsQuery locks the two events of a merge in id order, so
	// two merges that share an event cannot deadlock. A DRAFT event is not
	// returned.
	lockMergedEventsQuery = `
		SELECT e.id, e.venue_id, e.local_event_date, e.publish_state, e.listed_venue_name, e.other_listed_venue_names
		FROM events e
		WHERE e.id = ANY($1::uuid[]) AND ` + catalogEventCondition + `
		ORDER BY e.id
		FOR UPDATE
	`
	// mergePerformersQuery adds the source's performers to the target.
	mergePerformersQuery = `
		INSERT INTO concert_artists (event_id, artist_id)
		SELECT $2, ca.artist_id FROM concert_artists ca WHERE ca.event_id = $1
		ON CONFLICT DO NOTHING
	`
	// mergeTicketJourneysQuery moves the fans' ticket journeys of the source
	// to the target; a fan's journey for the target wins.
	mergeTicketJourneysQuery = `
		INSERT INTO ticket_journeys (user_id, event_id, status, status_set_by)
		SELECT tj.user_id, $2, tj.status, tj.status_set_by FROM ticket_journeys tj WHERE tj.event_id = $1
		ON CONFLICT (user_id, event_id) DO NOTHING
	`
	// mergeTicketSaleLinksQuery moves the source's links to ticket sales to
	// the target.
	mergeTicketSaleLinksQuery = `
		INSERT INTO ticket_sale_events (ticket_sale_id, event_id)
		SELECT tse.ticket_sale_id, $2 FROM ticket_sale_events tse WHERE tse.event_id = $1
		ON CONFLICT DO NOTHING
	`
	// deleteMergedEventQuery removes the source; its remaining performers,
	// concert row, ticket journeys and sale links cascade with it. Its Series
	// stays.
	deleteMergedEventQuery = `DELETE FROM events WHERE id = $1`
)

// Merge implements entity.ConcertRepository. It locks both events (D3 of the
// change recognize-same-discovered-concert), checks them, copies the
// source's performers, ticket journeys and sale links to the target, adds the
// source's names to the target's other listed venue names and deletes the
// source, in one transaction. A ticket journey or sale link written for the
// source after the lock waits for it, as its foreign key check takes a key
// share lock on the source row, and then fails instead of being removed.
func (r *ConcertRepository) Merge(ctx context.Context, sourceID, targetID string) error {
	attrs := []slog.Attr{slog.String("source_event_id", sourceID), slog.String("target_event_id", targetID)}
	if sourceID == targetID {
		return apperr.New(codes.InvalidArgument, "an event cannot be merged into itself", attrs...)
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin merge transaction", attrs...)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	source, target, err := lockMergedEvents(ctx, tx, sourceID, targetID)
	if err != nil {
		return err
	}
	if source.VenueID != target.VenueID || !source.LocalDate.Equal(target.LocalDate) {
		return apperr.New(codes.InvalidArgument, "merged events must be at the same venue and date", attrs...)
	}
	if source.PublishState != nil || target.PublishState != nil {
		return apperr.New(codes.FailedPrecondition, "an Organizer's event is never merged", attrs...)
	}

	for _, step := range []struct{ query, failure string }{
		{mergePerformersQuery, "failed to merge performers"},
		{mergeTicketJourneysQuery, "failed to merge ticket journeys"},
		{mergeTicketSaleLinksQuery, "failed to merge ticket sale links"},
	} {
		if _, err := tx.Exec(ctx, step.query, sourceID, targetID); err != nil {
			return toAppErr(err, step.failure, attrs...)
		}
	}
	names := target.OtherListedVenueNames
	for _, n := range sourceNames(source) {
		if !target.ListedUnder(n) {
			names = append(names, n)
			target.OtherListedVenueNames = names
		}
	}
	if _, err := tx.Exec(ctx, setOtherListedVenueNamesQuery, targetID, names); err != nil {
		return toAppErr(err, "failed to merge listed venue names", attrs...)
	}
	if _, err := tx.Exec(ctx, deleteMergedEventQuery, sourceID); err != nil {
		return toAppErr(err, "failed to delete merged event", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit merge", attrs...)
	}
	return nil
}

// lockMergedEvents locks and reads the source and target of a merge. It fails
// with NotFound when either is not an event that is not DRAFT.
func lockMergedEvents(ctx context.Context, tx pgx.Tx, sourceID, targetID string) (source, target *entity.Event, err error) {
	rows, err := tx.Query(ctx, lockMergedEventsQuery, []string{sourceID, targetID})
	if err != nil {
		return nil, nil, toAppErr(err, "failed to lock merged events")
	}
	defer rows.Close()
	for rows.Next() {
		var (
			e     entity.Event
			state sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.VenueID, &e.LocalDate, &state, &e.ListedVenueName, &e.OtherListedVenueNames); err != nil {
			return nil, nil, toAppErr(err, "failed to scan merged event")
		}
		e.PublishState = nullPublishState(state)
		switch e.ID {
		case sourceID:
			source = &e
		case targetID:
			target = &e
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, toAppErr(err, "merged events iteration ended with error")
	}
	if source == nil || target == nil {
		return nil, nil, apperr.New(codes.NotFound, "merged event not found",
			slog.String("source_event_id", sourceID), slog.String("target_event_id", targetID))
	}
	return source, target, nil
}

// sourceNames returns the listed venue name and the other listed venue names
// of a merged event.
func sourceNames(e *entity.Event) []string {
	var names []string
	if e.ListedVenueName != nil && *e.ListedVenueName != "" {
		names = append(names, *e.ListedVenueName)
	}
	return append(names, e.OtherListedVenueNames...)
}
