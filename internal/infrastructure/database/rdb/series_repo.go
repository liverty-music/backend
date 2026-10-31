package rdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// SeriesRepository implements [entity.SeriesRepository] for PostgreSQL.
type SeriesRepository struct {
	db     *Database
	outbox Announcer
}

// Compile-time interface compliance check.
var _ entity.SeriesRepository = (*SeriesRepository)(nil)

// NewSeriesRepository creates a new series repository instance.
func NewSeriesRepository(db *Database, outbox Announcer) *SeriesRepository {
	return &SeriesRepository{db: db, outbox: outbox}
}

const (
	insertSeriesQuery = `
		INSERT INTO series (id, title, type, source_url)
		SELECT * FROM unnest($1::uuid[], $2::text[], $3::series_type[], $4::text[])
		ON CONFLICT (id) DO NOTHING
		RETURNING id
	`

	// getSeriesQuery reads all series columns plus the cover image fields joined
	// from the series_images + series_cover_images tables. The cover image columns are nullable
	// (LEFT JOIN) so scanSeries uses sql.Null* for them.
	getSeriesQuery = `
		SELECT s.id, s.title, s.type, s.source_url,
		       s.description, s.organizer_id,
		       s.visibility, ` + seriesPublishStateColumn + `, s.unlisted_token,
		       m.id, m.organizer_id, m.attributes
		FROM series s
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		WHERE s.id = $1
	`

	listSeriesByIDsQuery = `
		SELECT s.id, s.title, s.type, s.source_url,
		       s.description, s.organizer_id,
		       s.visibility, ` + seriesPublishStateColumn + `, s.unlisted_token,
		       m.id, m.organizer_id, m.attributes
		FROM series s
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		WHERE s.id = ANY($1)
	`

	deleteOrphanedSeriesQuery = `
		DELETE FROM series s
		WHERE s.id = ANY($1)
			AND NOT EXISTS (SELECT 1 FROM events e WHERE e.series_id = s.id)
			AND NOT EXISTS (SELECT 1 FROM concert_candidates sc WHERE sc.series_id = s.id)
	`

	insertFirstPartySeriesQuery = `
		INSERT INTO series (id, title, type, source_url, description, organizer_id, visibility)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO NOTHING
	`

	// getAuthoredSeriesEventsQuery reads every event of an authored series,
	// whatever its publish state, with its Venue (id, name, admin area, place
	// id), so the editor can show and resend the picked place.
	getAuthoredSeriesEventsQuery = `
		SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at, e.publish_state,
		       e.per_account_limit, v.name, v.admin_area, v.google_place_id
		FROM events e
		JOIN venues v ON v.id = e.venue_id
		WHERE e.series_id = $1
		ORDER BY e.local_event_date, e.start_at NULLS LAST
	`

	// getAuthoredSeriesPerformersQuery reads the performers of every event of
	// an authored series, one row per (event, artist).
	getAuthoredSeriesPerformersQuery = `
		SELECT ep.event_id, a.id, a.name, a.mbid
		FROM artists a
		JOIN concert_artists ep ON ep.artist_id = a.id
		JOIN events e ON e.id = ep.event_id
		WHERE e.series_id = $1
		ORDER BY ep.event_id, a.id
	`

	listSeriesByOrganizerQuery = `
		SELECT s.id, s.title, s.type, s.source_url,
		       s.description, s.organizer_id,
		       s.visibility, ` + seriesPublishStateColumn + `, s.unlisted_token,
		       m.id, m.organizer_id, m.attributes
		FROM series s
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		WHERE s.organizer_id = $1
		ORDER BY s.id DESC
	`

	getSeriesByUnlistedTokenQuery = `
		SELECT s.id, s.title, s.type, s.source_url,
		       s.description, s.organizer_id,
		       s.visibility, ` + seriesPublishStateColumn + `, s.unlisted_token,
		       m.id, m.organizer_id, m.attributes
		FROM series s
		LEFT JOIN series_cover_images sm ON sm.series_id = s.id
		LEFT JOIN series_images m ON m.id = sm.series_image_id
		WHERE s.unlisted_token = $1
	`

	setUnlistedTokenQuery = `
		UPDATE series SET unlisted_token = $2 WHERE id = $1
	`

	dropConcertCandidatesBySeriesQuery = `
		DELETE FROM concert_candidates WHERE series_id = $1
	`

	// selectCoverImageIDQuery reads the current cover image id for a series via
	// the series_cover_images join. Returns pgx.ErrNoRows when no cover exists.
	selectCoverImageIDQuery = `
		SELECT sm.series_image_id FROM series_cover_images sm WHERE sm.series_id = $1
	`

	// deleteCoverImageBySeriesQuery removes the series_cover_images join row(s) and
	// then the orphaned series image row. Two statements are used in the transaction
	// so the FK constraint on series_cover_images.series_image_id is satisfied before the
	// series image row is deleted.
	deleteCoverImageRowQuery = `
		DELETE FROM series_images WHERE id = $1
	`

	deleteSeriesCoverImageBySeriesQuery = `
		DELETE FROM series_cover_images WHERE series_id = $1
	`

	insertSeriesImageQuery = `
		INSERT INTO series_images (id, organizer_id, attributes)
		VALUES ($1, $2, $3)
	`

	insertSeriesCoverImageQuery = `
		INSERT INTO series_cover_images (series_id, series_image_id)
		VALUES ($1, $2)
	`

	// insertSeriesImageIdempotentQuery inserts a series image row, silently skipping
	// conflicts on the primary key. Used by Create so SetCoverImage is
	// safe to call multiple times for the same upload.
	insertSeriesImageIdempotentQuery = `
		INSERT INTO series_images (id, organizer_id, attributes)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING
		RETURNING id
	`

	// getSeriesImageQuery retrieves a single series image row by its primary key.
	getSeriesImageQuery = `
		SELECT id, organizer_id, attributes
		FROM series_images
		WHERE id = $1
	`

	// deleteSeriesImageQuery removes a series image row by id. Missing rows are silently
	// skipped via the WHERE guard so DELETE is idempotent.
	deleteSeriesImageQuery = `DELETE FROM series_images WHERE id = $1`

	// listSeriesImagesByOrganizerQuery retrieves every series image row an organizer owns,
	// including rows no series uses as its cover.
	listSeriesImagesByOrganizerQuery = `
		SELECT id, organizer_id, attributes
		FROM series_images
		WHERE organizer_id = $1
		ORDER BY id
	`
)

// scanSeries scans a single series row (including the nullable first-party
// authoring columns and LEFT-JOINed cover image columns) into an entity.Series.
// The column order MUST match getSeriesQuery, listSeriesByIDsQuery,
// listSeriesByOrganizerQuery, and getSeriesByUnlistedTokenQuery.
// scanSeries scans one series row plus its (optional) single cover-image row
// from the LEFT JOIN series_cover_images/series_images used by the queries above. It assumes
// AT MOST ONE series image row per series, which the uq_series_cover_images_series unique
// index guarantees today. WARNING: if that unique index is ever relaxed for
// galleries, these joins fan out to N rows per series and callers that scan
// row-per-series (e.g. ListByIDs) will return duplicate entity.Series values —
// add dedup (group rows by series id) in the caller before relaxing it.
func scanSeries(scan func(dest ...any) error) (*entity.Series, error) {
	var (
		s            entity.Series
		seriesT      string
		sourceURL    sql.NullString
		description  sql.NullString
		organizerID  sql.NullString
		visibility   sql.NullString
		publishState sql.NullString
		unlistedTok  sql.NullString
		// Cover image columns (NULL when no cover exists).
		seriesImageID    sql.NullString
		imageOrganizerID sql.NullString
		imageAttrs       []byte // JSONB scanned as raw bytes; nil when no cover.
	)
	if err := scan(
		&s.ID, &s.Title, &seriesT, &sourceURL,
		&description, &organizerID,
		&visibility, &publishState, &unlistedTok,
		&seriesImageID, &imageOrganizerID, &imageAttrs,
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
	if organizerID.Valid {
		v := organizerID.String
		s.OrganizerID = &v
	}
	if visibility.Valid {
		v := entity.SeriesVisibility(visibility.String)
		s.Visibility = &v
	}
	s.PublishState = nullPublishState(publishState)
	if unlistedTok.Valid {
		v := unlistedTok.String
		s.UnlistedToken = &v
	}
	// Populate CoverImage when the LEFT JOIN produced a row.
	if seriesImageID.Valid {
		m := &entity.SeriesImage{
			ID:          seriesImageID.String,
			OrganizerID: imageOrganizerID.String,
		}
		if len(imageAttrs) > 0 {
			if err := json.Unmarshal(imageAttrs, &m.Attributes); err != nil {
				return nil, apperr.New(codes.Internal, fmt.Sprintf("unmarshal image attributes for series %s: %v", s.ID, err))
			}
		}
		s.CoverImage = m
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
// with no member events and no pending concert candidates referencing it. An empty
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

// queryAuthoredEvents reads every event of the series with its publish state
// and its Venue.
func (r *SeriesRepository) queryAuthoredEvents(ctx context.Context, q rowQuerier, seriesID string) ([]*entity.Event, error) {
	rows, err := q.Query(ctx, getAuthoredSeriesEventsQuery, seriesID)
	if err != nil {
		return nil, toAppErr(err, "failed to load authored events", slog.String("series_id", seriesID))
	}
	defer rows.Close()

	var events []*entity.Event
	for rows.Next() {
		var ev entity.Event
		var venue entity.Venue
		var lvn, state, adminArea, placeID sql.NullString
		var limit sql.NullInt32
		if err := rows.Scan(
			&ev.ID, &ev.SeriesID, &ev.VenueID, &lvn, &ev.LocalDate, &ev.StartTime, &ev.OpenTime, &state,
			&limit, &venue.Name, &adminArea, &placeID,
		); err != nil {
			return nil, toAppErr(err, "failed to scan authored event")
		}
		if lvn.Valid {
			v := lvn.String
			ev.ListedVenueName = &v
		}
		ev.PublishState = nullPublishState(state)
		ev.PerAccountLimit = optionalInt(limit)
		venue.ID = ev.VenueID
		if adminArea.Valid {
			v := adminArea.String
			venue.AdminArea = &v
		}
		if placeID.Valid {
			v := placeID.String
			venue.GooglePlaceID = &v
		}
		ev.Venue = &venue
		events = append(events, &ev)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "authored events iteration ended with error")
	}
	return events, nil
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

// ReplaceCoverImage atomically swaps a series' cover in a single transaction:
// it reads any prior cover image id, deletes the prior series_cover_images join row
// and its series image row, inserts the new series image row, and inserts the new
// series_cover_images join row. Returns the prior series image id ("" when none) so the
// caller can reclaim the replaced object from storage.
func (r *SeriesRepository) ReplaceCoverImage(ctx context.Context, seriesID string, newSeriesImage *entity.SeriesImage) (string, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return "", toAppErr(err, "failed to begin cover image transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read and hold the prior cover image id (may be absent).
	var oldSeriesImageID string
	err = tx.QueryRow(ctx, selectCoverImageIDQuery, seriesID).Scan(&oldSeriesImageID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", toAppErr(err, "failed to read existing cover image", slog.String("series_id", seriesID))
	}

	// Delete the prior series_cover_images join row first (FK constraint satisfied
	// before we delete the series image row).
	if _, err := tx.Exec(ctx, deleteSeriesCoverImageBySeriesQuery, seriesID); err != nil {
		return "", toAppErr(err, "failed to delete prior series_cover_images row", slog.String("series_id", seriesID))
	}
	// Delete the prior series image row if one existed.
	if oldSeriesImageID != "" {
		if _, err := tx.Exec(ctx, deleteCoverImageRowQuery, oldSeriesImageID); err != nil {
			return "", toAppErr(err, "failed to delete prior series image row",
				slog.String("series_id", seriesID),
				slog.String("old_series_image_id", oldSeriesImageID),
			)
		}
	}

	// Marshal the attributes map to JSONB.
	attrsJSON, err := json.Marshal(newSeriesImage.Attributes)
	if err != nil {
		return "", apperr.New(codes.Internal, fmt.Sprintf("marshal image attributes: %v", err))
	}

	// Insert the new series image row then the join row.
	if _, err := tx.Exec(ctx, insertSeriesImageQuery,
		newSeriesImage.ID, newSeriesImage.OrganizerID, attrsJSON,
	); err != nil {
		return "", toAppErr(err, "failed to insert series image row", slog.String("series_image_id", newSeriesImage.ID))
	}
	if _, err := tx.Exec(ctx, insertSeriesCoverImageQuery, seriesID, newSeriesImage.ID); err != nil {
		// A foreign-key violation on series_id means the series does not exist;
		// the series_cover_images FK is the sole existence check (no extra round-trip).
		if IsForeignKeyViolation(err) {
			return "", apperr.New(codes.NotFound, "series not found")
		}
		return "", toAppErr(err, "failed to insert series_cover_images row", slog.String("series_id", seriesID))
	}

	if err := tx.Commit(ctx); err != nil {
		return "", toAppErr(err, "failed to commit cover image transaction")
	}
	return oldSeriesImageID, nil
}
