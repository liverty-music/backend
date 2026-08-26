package rdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// SeriesRepository implements [entity.SeriesRepository] for PostgreSQL.
type SeriesRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.SeriesRepository = (*SeriesRepository)(nil)

// NewSeriesRepository creates a new series repository instance.
func NewSeriesRepository(db *Database) *SeriesRepository {
	return &SeriesRepository{db: db}
}

const (
	insertSeriesQuery = `
		INSERT INTO series (id, title, type, source_url)
		SELECT * FROM unnest($1::uuid[], $2::text[], $3::series_type[], $4::text[])
		ON CONFLICT (id) DO NOTHING
		RETURNING id
	`

	// getSeriesQuery reads all columns including the first-party authoring fields.
	// Nullable authoring fields are scanned into sql.Null* to guard against NULL.
	getSeriesQuery = `
		SELECT id, title, type, source_url,
		       description, cover_image_url, organizer_id,
		       visibility, publish_state, unlisted_token,
		       published_at, cancelled_at
		FROM series
		WHERE id = $1
	`

	listSeriesByIDsQuery = `
		SELECT id, title, type, source_url,
		       description, cover_image_url, organizer_id,
		       visibility, publish_state, unlisted_token,
		       published_at, cancelled_at
		FROM series
		WHERE id = ANY($1)
	`

	deleteOrphanedSeriesQuery = `
		DELETE FROM series s
		WHERE s.id = ANY($1)
			AND NOT EXISTS (SELECT 1 FROM events e WHERE e.series_id = s.id)
			AND NOT EXISTS (SELECT 1 FROM staged_concerts sc WHERE sc.series_id = s.id)
	`

	insertFirstPartySeriesQuery = `
		INSERT INTO series (id, title, type, source_url, description, organizer_id, visibility, publish_state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING
	`

	insertDraftEventQuery = `
		INSERT INTO draft_events (id, series_id, venue_id, listed_venue_name, local_event_date, start_at, open_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	insertDraftPerformerQuery = `
		INSERT INTO draft_series_performers (series_id, artist_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`

	deleteDraftEventsQuery = `
		DELETE FROM draft_events WHERE series_id = $1
	`

	deleteDraftPerformersQuery = `
		DELETE FROM draft_series_performers WHERE series_id = $1
	`

	updateDraftSeriesQuery = `
		UPDATE series
		SET title = $2, type = $3, source_url = $4, description = $5, visibility = $6
		WHERE id = $1 AND publish_state = 'DRAFT' AND organizer_id IS NOT NULL
	`

	loadDraftEventsQuery = `
		SELECT id, series_id, venue_id, listed_venue_name, local_event_date, start_at, open_at
		FROM draft_events
		WHERE series_id = $1
		ORDER BY local_event_date, start_at NULLS LAST
	`

	loadDraftPerformersQuery = `
		SELECT artist_id FROM draft_series_performers WHERE series_id = $1
	`

	getAuthoredSeriesLiveEventsQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at
		FROM events e
		WHERE e.series_id = $1
		ORDER BY e.local_event_date, e.start_at NULLS LAST
	`

	getAuthoredSeriesPerformersQuery = `
		SELECT DISTINCT a.id, a.name, a.mbid
		FROM artists a
		JOIN event_performers ep ON ep.artist_id = a.id
		JOIN events e ON e.id = ep.event_id
		WHERE e.series_id = $1
		ORDER BY a.id
	`

	getAuthoredSeriesDraftPerformersQuery = `
		SELECT a.id, a.name, a.mbid
		FROM artists a
		JOIN draft_series_performers dsp ON dsp.artist_id = a.id
		WHERE dsp.series_id = $1
		ORDER BY a.id
	`

	listSeriesByOrganizerQuery = `
		SELECT id, title, type, source_url,
		       description, cover_image_url, organizer_id,
		       visibility, publish_state, unlisted_token,
		       published_at, cancelled_at
		FROM series
		WHERE organizer_id = $1
		ORDER BY id DESC
	`

	getSeriesByUnlistedTokenQuery = `
		SELECT id, title, type, source_url,
		       description, cover_image_url, organizer_id,
		       visibility, publish_state, unlisted_token,
		       published_at, cancelled_at
		FROM series
		WHERE unlisted_token = $1
	`

	setUnlistedTokenQuery = `
		UPDATE series SET unlisted_token = $2 WHERE id = $1
	`

	markSeriesPublishedQuery = `
		UPDATE series
		SET publish_state = 'PUBLISHED', published_at = $2
		WHERE id = $1
	`

	markSeriesCancelledQuery = `
		UPDATE series
		SET publish_state = 'CANCELLED', cancelled_at = $2
		WHERE id = $1
	`

	dropStagedConcertsBySeriesQuery = `
		DELETE FROM staged_concerts WHERE series_id = $1
	`

	selectCoverMediaIDQuery = `
		SELECT id FROM series_media WHERE series_id = $1
	`

	deleteCoverMediaBySeriesQuery = `
		DELETE FROM series_media WHERE series_id = $1
	`

	insertCoverMediaQuery = `
		INSERT INTO series_media (id, series_id) VALUES ($1, $2)
	`

	setCoverImageURLQuery = `
		UPDATE series SET cover_image_url = $2 WHERE id = $1
	`
)

// scanSeries scans a single series row (including the nullable first-party
// authoring columns) into an entity.Series. The column order MUST match
// getSeriesQuery, listSeriesByIDsQuery, listSeriesByOrganizerQuery, and
// getSeriesByUnlistedTokenQuery.
func scanSeries(scan func(dest ...any) error) (*entity.Series, error) {
	var (
		s            entity.Series
		seriesT      string
		sourceURL    sql.NullString
		description  sql.NullString
		coverImage   sql.NullString
		organizerID  sql.NullString
		visibility   sql.NullString
		publishState sql.NullString
		unlistedTok  sql.NullString
		publishedAt  sql.NullTime
		cancelledAt  sql.NullTime
	)
	if err := scan(
		&s.ID, &s.Title, &seriesT, &sourceURL,
		&description, &coverImage, &organizerID,
		&visibility, &publishState, &unlistedTok,
		&publishedAt, &cancelledAt,
	); err != nil {
		return nil, err
	}
	if err := assignSeriesType(&s, seriesT, s.ID); err != nil {
		return nil, err
	}
	if sourceURL.Valid {
		s.SourceURL = sourceURL.String
	}
	if description.Valid {
		v := description.String
		s.Description = &v
	}
	if coverImage.Valid {
		v := coverImage.String
		s.CoverImageURL = &v
	}
	if organizerID.Valid {
		v := organizerID.String
		s.OrganizerID = &v
	}
	if visibility.Valid {
		v := entity.SeriesVisibility(visibility.String)
		s.Visibility = &v
	}
	if publishState.Valid {
		v := entity.SeriesPublishState(publishState.String)
		s.PublishState = &v
	}
	if unlistedTok.Valid {
		v := unlistedTok.String
		s.UnlistedToken = &v
	}
	if publishedAt.Valid {
		v := publishedAt.Time
		s.PublishedAt = &v
	}
	if cancelledAt.Valid {
		v := cancelledAt.Time
		s.CancelledAt = &v
	}
	return &s, nil
}

// Create persists one or more series rows. Nil elements are skipped silently.
// Returns the IDs that were genuinely inserted (rows that hit ON CONFLICT DO
// NOTHING are excluded from the result).
func (r *SeriesRepository) Create(ctx context.Context, series ...*entity.Series) ([]string, error) {
	if len(series) == 0 {
		return nil, nil
	}

	var valid []*entity.Series
	for _, s := range series {
		if s != nil {
			valid = append(valid, s)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}

	n := len(valid)
	ids := make([]string, n)
	titles := make([]string, n)
	types := make([]string, n)
	sourceURLs := make([]*string, n)

	for i, s := range valid {
		if s.ID == "" {
			return nil, apperr.New(codes.InvalidArgument, "series ID must not be empty")
		}
		if s.Title == "" {
			return nil, apperr.New(codes.InvalidArgument, "series title must not be empty")
		}
		if s.Type == "" {
			return nil, apperr.New(codes.InvalidArgument, "series type must not be empty")
		}
		switch s.Type {
		case entity.SeriesTypeTour, entity.SeriesTypeSingle, entity.SeriesTypeFestival:
			// valid
		default:
			return nil, apperr.New(codes.InvalidArgument, "series type is not a recognised value: "+string(s.Type))
		}
		ids[i] = s.ID
		titles[i] = s.Title
		types[i] = string(s.Type)
		if s.SourceURL != "" {
			url := s.SourceURL
			sourceURLs[i] = &url
		}
	}

	rows, err := r.db.Pool.Query(ctx, insertSeriesQuery, ids, titles, types, sourceURLs)
	if err != nil {
		return nil, toAppErr(err, "failed to insert series", slog.Int("count", n))
	}
	defer rows.Close()

	insertedIDs := make([]string, 0, n)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, toAppErr(err, "failed to scan inserted series id")
		}
		insertedIDs = append(insertedIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "series insert RETURNING iteration ended with error",
			slog.Int("count", n),
		)
	}
	// `ON CONFLICT (id) DO NOTHING` silently drops the caller's title +
	// source_url when a series row with the same content-addressed id
	// already exists (the co-headliner case: artist B's discovery
	// re-computes the same UUID v5 as artist A's earlier run). Surface
	// the discard so ops can investigate if the rejected count is
	// abnormally high — typical re-deliveries account for a few %, but
	// a sustained gap suggests upstream data churn or a key-derivation
	// regression.
	if len(insertedIDs) < n {
		r.db.logger.Warn(ctx, "some series rows already existed; title/source_url from caller not applied",
			slog.Int("submitted", n),
			slog.Int("inserted", len(insertedIDs)),
		)
	}
	return insertedIDs, nil
}

// DeleteOrphaned removes, from among the given candidate series IDs, every row
// with no member events and no pending staged concerts referencing it. An empty
// slice is a no-op. Returns the number of rows deleted.
func (r *SeriesRepository) DeleteOrphaned(ctx context.Context, seriesIDs []string) (int64, error) {
	if len(seriesIDs) == 0 {
		return 0, nil
	}
	tag, err := r.db.Pool.Exec(ctx, deleteOrphanedSeriesQuery, seriesIDs)
	if err != nil {
		return 0, toAppErr(err, "failed to delete orphaned series", slog.Int("count", len(seriesIDs)))
	}
	return tag.RowsAffected(), nil
}

// Get retrieves a series by its ID. Returns apperr.ErrNotFound if no row exists.
func (r *SeriesRepository) Get(ctx context.Context, id string) (*entity.Series, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "series ID must not be empty")
	}

	s, err := scanSeries(r.db.Pool.QueryRow(ctx, getSeriesQuery, id).Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get series", slog.String("series_id", id))
	}
	return s, nil
}

// assignSeriesType validates the raw DB series_type string against the Go
// allowlist before assigning it to the entity. Without this guard a value
// added to the Postgres `series_type` enum before the binary is updated
// (e.g. a future `RESIDENCY`) would silently collapse to UNSPECIFIED at
// the proto mapper, and the version skew would only surface at the RPC
// boundary. Same logic as scanConcertRow in concert_repo.go — kept here
// so Get / ListByIDs share the fail-fast contract.
func assignSeriesType(s *entity.Series, raw, seriesID string) error {
	switch entity.SeriesType(raw) {
	case entity.SeriesTypeTour, entity.SeriesTypeSingle, entity.SeriesTypeFestival:
		s.Type = entity.SeriesType(raw)
		return nil
	default:
		return apperr.New(codes.Internal,
			"unknown series_type from DB — Go binary may be behind a Postgres enum extension",
			slog.String("series_id", seriesID),
			slog.String("series_type", raw),
		)
	}
}

// ListByIDs retrieves multiple series by ID. IDs not found are silently omitted.
func (r *SeriesRepository) ListByIDs(ctx context.Context, ids []string) ([]*entity.Series, error) {
	if len(ids) == 0 {
		return nil, apperr.New(codes.InvalidArgument, "series IDs must not be empty")
	}

	rows, err := r.db.Pool.Query(ctx, listSeriesByIDsQuery, ids)
	if err != nil {
		return nil, toAppErr(err, "failed to list series by IDs", slog.Int("count", len(ids)))
	}
	defer rows.Close()

	var result []*entity.Series
	for rows.Next() {
		s, err := scanSeries(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan series")
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "series list iteration ended with error",
			slog.Int("count", len(ids)),
		)
	}
	return result, nil
}

// CreateDraft persists a new first-party series in the DRAFT state along with
// its draft events and draft performers in a single transaction.
func (r *SeriesRepository) CreateDraft(ctx context.Context, series *entity.Series, draftEvents []*entity.DraftEvent, performerArtistIDs []string) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var descPtr *string
	if series.Description != nil {
		descPtr = series.Description
	}
	var sourceURLPtr *string
	if series.SourceURL != "" {
		sourceURLPtr = &series.SourceURL
	}
	var visStr *string
	if series.Visibility != nil {
		v := string(*series.Visibility)
		visStr = &v
	}

	if _, err := tx.Exec(ctx, insertFirstPartySeriesQuery,
		series.ID, series.Title, string(series.Type), sourceURLPtr,
		descPtr, series.OrganizerID, visStr, string(entity.SeriesPublishStateDraft),
	); err != nil {
		return toAppErr(err, "failed to insert first-party series draft", slog.String("series_id", series.ID))
	}

	for _, de := range draftEvents {
		var lvn *string
		if de.ListedVenueName != nil {
			lvn = de.ListedVenueName
		}
		if _, err := tx.Exec(ctx, insertDraftEventQuery,
			de.ID, de.SeriesID, de.VenueID, lvn, de.LocalDate, de.StartTime, de.OpenTime,
		); err != nil {
			return toAppErr(err, "failed to insert draft event", slog.String("series_id", series.ID))
		}
	}

	for _, artistID := range performerArtistIDs {
		if _, err := tx.Exec(ctx, insertDraftPerformerQuery, series.ID, artistID); err != nil {
			return toAppErr(err, "failed to insert draft performer",
				slog.String("series_id", series.ID),
				slog.String("artist_id", artistID),
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit draft creation transaction")
	}
	return nil
}

// UpdateDraft replaces draft content for a DRAFT series in a single transaction.
// The update is rejected (0 rows) when the series is not in the DRAFT state.
func (r *SeriesRepository) UpdateDraft(ctx context.Context, series *entity.Series, draftEvents []*entity.DraftEvent, performerArtistIDs []string) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var descPtr *string
	if series.Description != nil {
		descPtr = series.Description
	}
	var sourceURLPtr *string
	if series.SourceURL != "" {
		sourceURLPtr = &series.SourceURL
	}
	var visStr *string
	if series.Visibility != nil {
		v := string(*series.Visibility)
		visStr = &v
	}

	tag, err := tx.Exec(ctx, updateDraftSeriesQuery,
		series.ID, series.Title, string(series.Type), sourceURLPtr, descPtr, visStr,
	)
	if err != nil {
		return toAppErr(err, "failed to update draft series", slog.String("series_id", series.ID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.FailedPrecondition, "series is not in DRAFT state or does not exist")
	}

	if _, err := tx.Exec(ctx, deleteDraftEventsQuery, series.ID); err != nil {
		return toAppErr(err, "failed to delete draft events", slog.String("series_id", series.ID))
	}
	if _, err := tx.Exec(ctx, deleteDraftPerformersQuery, series.ID); err != nil {
		return toAppErr(err, "failed to delete draft performers", slog.String("series_id", series.ID))
	}

	for _, de := range draftEvents {
		var lvn *string
		if de.ListedVenueName != nil {
			lvn = de.ListedVenueName
		}
		if _, err := tx.Exec(ctx, insertDraftEventQuery,
			de.ID, de.SeriesID, de.VenueID, lvn, de.LocalDate, de.StartTime, de.OpenTime,
		); err != nil {
			return toAppErr(err, "failed to insert draft event on update", slog.String("series_id", series.ID))
		}
	}
	for _, artistID := range performerArtistIDs {
		if _, err := tx.Exec(ctx, insertDraftPerformerQuery, series.ID, artistID); err != nil {
			return toAppErr(err, "failed to insert draft performer on update",
				slog.String("series_id", series.ID),
				slog.String("artist_id", artistID),
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit draft update transaction")
	}
	return nil
}

// LoadDraft retrieves the draft events and performer artist IDs for the given series.
func (r *SeriesRepository) LoadDraft(ctx context.Context, seriesID string) ([]*entity.DraftEvent, []string, error) {
	evRows, err := r.db.Pool.Query(ctx, loadDraftEventsQuery, seriesID)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to load draft events", slog.String("series_id", seriesID))
	}
	defer evRows.Close()

	var draftEvents []*entity.DraftEvent
	for evRows.Next() {
		var de entity.DraftEvent
		var lvn sql.NullString
		if err := evRows.Scan(&de.ID, &de.SeriesID, &de.VenueID, &lvn, &de.LocalDate, &de.StartTime, &de.OpenTime); err != nil {
			return nil, nil, toAppErr(err, "failed to scan draft event")
		}
		if lvn.Valid {
			v := lvn.String
			de.ListedVenueName = &v
		}
		draftEvents = append(draftEvents, &de)
	}
	if err := evRows.Err(); err != nil {
		return nil, nil, toAppErr(err, "draft events iteration ended with error")
	}
	evRows.Close()

	perfRows, err := r.db.Pool.Query(ctx, loadDraftPerformersQuery, seriesID)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to load draft performers", slog.String("series_id", seriesID))
	}
	defer perfRows.Close()

	var performerIDs []string
	for perfRows.Next() {
		var artistID string
		if err := perfRows.Scan(&artistID); err != nil {
			return nil, nil, toAppErr(err, "failed to scan draft performer")
		}
		performerIDs = append(performerIDs, artistID)
	}
	if err := perfRows.Err(); err != nil {
		return nil, nil, toAppErr(err, "draft performers iteration ended with error")
	}
	return draftEvents, performerIDs, nil
}

// GetAuthored retrieves the full authored concert for one series.
func (r *SeriesRepository) GetAuthored(ctx context.Context, seriesID string) (*entity.Series, []*entity.Event, []*entity.Artist, error) {
	s, err := scanSeries(r.db.Pool.QueryRow(ctx, getSeriesQuery, seriesID).Scan)
	if err != nil {
		return nil, nil, nil, toAppErr(err, "failed to get authored series", slog.String("series_id", seriesID))
	}

	isDraft := s.PublishState != nil && *s.PublishState == entity.SeriesPublishStateDraft

	var events []*entity.Event
	if isDraft {
		// Return draft events as Event structs for a uniform response shape.
		draftEvs, _, err := r.LoadDraft(ctx, seriesID)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, de := range draftEvs {
			ev := &entity.Event{
				ID:              de.ID,
				SeriesID:        de.SeriesID,
				VenueID:         de.VenueID,
				ListedVenueName: de.ListedVenueName,
				LocalDate:       de.LocalDate,
				StartTime:       de.StartTime,
				OpenTime:        de.OpenTime,
			}
			events = append(events, ev)
		}
	} else {
		evRows, err := r.db.Pool.Query(ctx, getAuthoredSeriesLiveEventsQuery, seriesID)
		if err != nil {
			return nil, nil, nil, toAppErr(err, "failed to load live events", slog.String("series_id", seriesID))
		}
		defer evRows.Close()
		for evRows.Next() {
			var ev entity.Event
			var lvn sql.NullString
			if err := evRows.Scan(&ev.ID, &ev.SeriesID, &ev.VenueID, &lvn, &ev.LocalDate, &ev.StartTime, &ev.OpenTime); err != nil {
				return nil, nil, nil, toAppErr(err, "failed to scan authored live event")
			}
			if lvn.Valid {
				v := lvn.String
				ev.ListedVenueName = &v
			}
			events = append(events, &ev)
		}
		if err := evRows.Err(); err != nil {
			return nil, nil, nil, toAppErr(err, "live events iteration ended with error")
		}
	}

	performersQuery := getAuthoredSeriesPerformersQuery
	if isDraft {
		performersQuery = getAuthoredSeriesDraftPerformersQuery
	}
	perfRows, err := r.db.Pool.Query(ctx, performersQuery, seriesID)
	if err != nil {
		return nil, nil, nil, toAppErr(err, "failed to load authored performers", slog.String("series_id", seriesID))
	}
	defer perfRows.Close()

	var artists []*entity.Artist
	for perfRows.Next() {
		var a entity.Artist
		if err := perfRows.Scan(&a.ID, &a.Name, &a.MBID); err != nil {
			return nil, nil, nil, toAppErr(err, "failed to scan authored performer")
		}
		artists = append(artists, &a)
	}
	if err := perfRows.Err(); err != nil {
		return nil, nil, nil, toAppErr(err, "performers iteration ended with error")
	}

	return s, events, artists, nil
}

// ListByOrganizer retrieves all first-party series owned by the given organizer.
func (r *SeriesRepository) ListByOrganizer(ctx context.Context, organizerID string) ([]*entity.Series, error) {
	if organizerID == "" {
		return nil, apperr.New(codes.InvalidArgument, "organizer ID must not be empty")
	}
	rows, err := r.db.Pool.Query(ctx, listSeriesByOrganizerQuery, organizerID)
	if err != nil {
		return nil, toAppErr(err, "failed to list series by organizer", slog.String("organizer_id", organizerID))
	}
	defer rows.Close()

	var result []*entity.Series
	for rows.Next() {
		s, err := scanSeries(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan series by organizer")
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "series by organizer iteration ended with error")
	}
	return result, nil
}

// GetByUnlistedToken retrieves a first-party series by its share token.
func (r *SeriesRepository) GetByUnlistedToken(ctx context.Context, token string) (*entity.Series, error) {
	s, err := scanSeries(r.db.Pool.QueryRow(ctx, getSeriesByUnlistedTokenQuery, token).Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get series by unlisted token")
	}
	return s, nil
}

// SetUnlistedToken sets the unlisted_token for the given series.
func (r *SeriesRepository) SetUnlistedToken(ctx context.Context, seriesID, token string) error {
	tag, err := r.db.Pool.Exec(ctx, setUnlistedTokenQuery, seriesID, token)
	if err != nil {
		return toAppErr(err, "failed to set unlisted token", slog.String("series_id", seriesID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "series not found")
	}
	return nil
}

// MarkPublished transitions the series to PUBLISHED and records published_at.
func (r *SeriesRepository) MarkPublished(ctx context.Context, seriesID string, publishedAt time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, markSeriesPublishedQuery, seriesID, publishedAt)
	if err != nil {
		return toAppErr(err, "failed to mark series published", slog.String("series_id", seriesID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "series not found")
	}
	return nil
}

// ReplaceCoverMedia atomically swaps a series' cover media in one transaction:
// it reads any prior cover media id, deletes the prior row, inserts the new
// media row, and denormalizes coverURL onto series.cover_image_url. It returns
// the prior media id ("" when the series had no cover) so the caller can reclaim
// the replaced object from storage.
func (r *SeriesRepository) ReplaceCoverMedia(ctx context.Context, seriesID, newMediaID, coverURL string) (string, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return "", toAppErr(err, "failed to begin cover media transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var oldMediaID string
	err = tx.QueryRow(ctx, selectCoverMediaIDQuery, seriesID).Scan(&oldMediaID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", toAppErr(err, "failed to read existing cover media", slog.String("series_id", seriesID))
	}

	if _, err := tx.Exec(ctx, deleteCoverMediaBySeriesQuery, seriesID); err != nil {
		return "", toAppErr(err, "failed to delete prior cover media", slog.String("series_id", seriesID))
	}
	if _, err := tx.Exec(ctx, insertCoverMediaQuery, newMediaID, seriesID); err != nil {
		return "", toAppErr(err, "failed to insert cover media", slog.String("series_id", seriesID))
	}

	tag, err := tx.Exec(ctx, setCoverImageURLQuery, seriesID, coverURL)
	if err != nil {
		return "", toAppErr(err, "failed to set cover image URL", slog.String("series_id", seriesID))
	}
	if tag.RowsAffected() == 0 {
		return "", apperr.New(codes.NotFound, "series not found")
	}

	if err := tx.Commit(ctx); err != nil {
		return "", toAppErr(err, "failed to commit cover media transaction")
	}
	return oldMediaID, nil
}

// MarkCancelled transitions the series to CANCELLED and records cancelled_at.
func (r *SeriesRepository) MarkCancelled(ctx context.Context, seriesID string, cancelledAt time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, markSeriesCancelledQuery, seriesID, cancelledAt)
	if err != nil {
		return toAppErr(err, "failed to mark series cancelled", slog.String("series_id", seriesID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "series not found")
	}
	return nil
}

// PublishDraft materializes the draft events of a first-party DRAFT series into
// the live events table in a single transaction, applying the same
// suppression / duplicate / claim / cross-organizer decision tree as the
// discovery pipeline.
func (r *SeriesRepository) PublishDraft(ctx context.Context, seriesID string, now time.Time) ([]string, error) {
	// Load the draft content outside the transaction (read-only, no locking needed).
	draftEvents, performerIDs, err := r.LoadDraft(ctx, seriesID)
	if err != nil {
		return nil, err
	}

	// Load the series to verify state and get the organizer id.
	series, err := r.Get(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if series.PublishState == nil || *series.PublishState != entity.SeriesPublishStateDraft {
		return nil, apperr.New(codes.FailedPrecondition, "series is not in DRAFT state")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin publish transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var insertedEventIDs []string

	for _, de := range draftEvents {
		// Check suppression: a previously-deleted-then-re-authored natural key is
		// blocked. In this MVP there is no override flag — treat suppressed as a
		// publish error.
		var suppressed bool
		err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM suppressed_concerts
				WHERE venue_id = $1
				  AND local_event_date = $2
				  AND start_at IS NOT DISTINCT FROM $3
			)`, de.VenueID, de.LocalDate, de.StartTime,
		).Scan(&suppressed)
		if err != nil {
			return nil, toAppErr(err, "failed to check suppression during publish")
		}
		if suppressed {
			return nil, apperr.New(codes.FailedPrecondition,
				"publish blocked: one or more event slots are suppressed",
				slog.String("series_id", seriesID),
				slog.String("venue_id", de.VenueID),
				slog.String("local_date", de.LocalDate.Format("2006-01-02")),
			)
		}

		// Check for an existing event at this natural key.
		var existingID, existingSeriesID string
		var existingOrganizerIDNull sql.NullString
		err = tx.QueryRow(ctx, `
			SELECT e.id, e.series_id, s.organizer_id
			FROM events e
			JOIN series s ON s.id = e.series_id
			WHERE e.venue_id = $1
			  AND e.local_event_date = $2
			  AND e.start_at IS NOT DISTINCT FROM $3
			LIMIT 1`,
			de.VenueID, de.LocalDate, de.StartTime,
		).Scan(&existingID, &existingSeriesID, &existingOrganizerIDNull)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, toAppErr(err, "failed to check existing event during publish")
			}
			// pgx.ErrNoRows: no conflict — fall through to insert.
			err = nil
		}

		if existingID != "" {
			// Determine ownership of the existing event's series.
			existingIsOtherOrg := existingOrganizerIDNull.Valid &&
				existingOrganizerIDNull.String != "" &&
				existingOrganizerIDNull.String != *series.OrganizerID

			if existingIsOtherOrg {
				// Cross-organizer collision: another organizer already owns this slot.
				return nil, apperr.New(codes.FailedPrecondition,
					"publish blocked: event slot already claimed by another organizer",
					slog.String("series_id", seriesID),
					slog.String("conflicting_event_id", existingID),
					slog.String("conflicting_organizer_id", existingOrganizerIDNull.String),
				)
			}

			if existingSeriesID == seriesID {
				// Already belongs to THIS series — no-op.
				continue
			}
		}

		if existingID != "" {
			// Claim the discovered event: re-point its series_id to the organizer's series.
			// The existing event belongs to a discovered (no-organizer) series.
			if _, err := tx.Exec(ctx, `UPDATE events SET series_id = $1 WHERE id = $2`, seriesID, existingID); err != nil {
				return nil, toAppErr(err, "failed to claim existing event during publish",
					slog.String("event_id", existingID),
				)
			}
			// Attach performers to the claimed event.
			for _, artistID := range performerIDs {
				if _, err := tx.Exec(ctx,
					`INSERT INTO event_performers (event_id, artist_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
					existingID, artistID,
				); err != nil {
					return nil, toAppErr(err, "failed to link performer to claimed event")
				}
			}
			// Do not re-emit CONCERT.created for a claimed event (no double-notify).
			continue
		}

		// Genuinely new event: insert it.
		eventID := entity.NewID()
		if _, err := tx.Exec(ctx, `
			INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date, start_at, open_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT ON CONSTRAINT uq_events_natural_key DO NOTHING`,
			eventID, seriesID, de.VenueID, de.ListedVenueName, de.LocalDate, de.StartTime, de.OpenTime,
		); err != nil {
			return nil, toAppErr(err, "failed to insert event during publish")
		}

		// Check whether the event actually landed (ON CONFLICT might have absorbed it).
		var landed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id = $1)`, eventID).Scan(&landed); err != nil {
			return nil, toAppErr(err, "failed to verify event insert during publish")
		}
		if !landed {
			continue
		}

		// Insert the concerts placeholder row.
		if _, err := tx.Exec(ctx, `INSERT INTO concerts (event_id) VALUES ($1) ON CONFLICT DO NOTHING`, eventID); err != nil {
			return nil, toAppErr(err, "failed to insert concerts row during publish")
		}

		// Attach performers.
		for _, artistID := range performerIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO event_performers (event_id, artist_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				eventID, artistID,
			); err != nil {
				return nil, toAppErr(err, "failed to link performer to new event during publish")
			}
		}
		insertedEventIDs = append(insertedEventIDs, eventID)
	}

	// Delete draft rows — they have been materialized.
	if _, err := tx.Exec(ctx, deleteDraftEventsQuery, seriesID); err != nil {
		return nil, toAppErr(err, "failed to delete draft events after publish")
	}
	if _, err := tx.Exec(ctx, deleteDraftPerformersQuery, seriesID); err != nil {
		return nil, toAppErr(err, "failed to delete draft performers after publish")
	}

	// Drop staged concerts that referenced this series (superseded by organizer version).
	if _, err := tx.Exec(ctx, dropStagedConcertsBySeriesQuery, seriesID); err != nil {
		return nil, toAppErr(err, "failed to drop staged concerts for series")
	}

	// Mark the series PUBLISHED.
	if _, err := tx.Exec(ctx, markSeriesPublishedQuery, seriesID, now); err != nil {
		return nil, toAppErr(err, "failed to mark series published in publish transaction")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit publish transaction")
	}
	return insertedEventIDs, nil
}
