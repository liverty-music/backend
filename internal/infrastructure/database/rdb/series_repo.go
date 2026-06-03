package rdb

import (
	"context"
	"log/slog"
	"time"

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
		INSERT INTO series (id, title, type, source_url, merch_url)
		SELECT * FROM unnest($1::uuid[], $2::text[], $3::series_type[], $4::text[], $5::text[])
		ON CONFLICT (id) DO NOTHING
		RETURNING id
	`

	getSeriesQuery = `
		SELECT id, title, type, source_url, merch_url
		FROM series
		WHERE id = $1
	`

	listSeriesByIDsQuery = `
		SELECT id, title, type, source_url, merch_url
		FROM series
		WHERE id = ANY($1)
	`

	// listSeriesInMerchWindowQuery returns every series whose earliest event's
	// local date sits within [today, today + $1 days]. The correlated subquery
	// picks a single representative performer of that earliest event (stable
	// ordering on date then artist id) to ground the merch search prompt.
	// merch_url is returned as-is so the caller can partition into search
	// (empty) and revalidation (non-empty) sets. Coalesced to '' so the Go
	// side never has to scan a nullable.
	listSeriesInMerchWindowQuery = `
		WITH series_window AS (
			SELECT e.series_id, MIN(e.local_event_date) AS earliest_date
			FROM events e
			GROUP BY e.series_id
			HAVING MIN(e.local_event_date) >= CURRENT_DATE
			   AND MIN(e.local_event_date) <= CURRENT_DATE + make_interval(days => $1)
		)
		SELECT s.id, s.title, COALESCE(s.merch_url, ''),
		       COALESCE((
		           SELECT a.name
		           FROM events e
		           JOIN event_performers ep ON ep.event_id = e.id
		           JOIN artists a ON a.id = ep.artist_id
		           WHERE e.series_id = s.id
		           ORDER BY e.local_event_date ASC, a.id ASC
		           LIMIT 1
		       ), '') AS artist_name
		FROM series s
		JOIN series_window sw ON sw.series_id = s.id
		ORDER BY sw.earliest_date ASC
	`

	// setMerchURLQuery enforces fill-once at the database layer: the UPDATE only
	// touches rows whose merch_url is currently NULL, so a live URL is never
	// overwritten even if the application-level guard is bypassed.
	setMerchURLQuery = `
		UPDATE series
		SET merch_url = $2
		WHERE id = $1 AND merch_url IS NULL
	`

	clearMerchURLQuery = `
		UPDATE series
		SET merch_url = NULL
		WHERE id = $1
	`
)

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
	merchURLs := make([]*string, n)

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
		if s.MerchURL != "" {
			url := s.MerchURL
			merchURLs[i] = &url
		}
	}

	rows, err := r.db.Pool.Query(ctx, insertSeriesQuery, ids, titles, types, sourceURLs, merchURLs)
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

// Get retrieves a series by its ID. Returns apperr.ErrNotFound if no row exists.
func (r *SeriesRepository) Get(ctx context.Context, id string) (*entity.Series, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "series ID must not be empty")
	}

	var (
		s         entity.Series
		seriesT   string
		sourceURL *string
		merchURL  *string
	)
	err := r.db.Pool.QueryRow(ctx, getSeriesQuery, id).Scan(&s.ID, &s.Title, &seriesT, &sourceURL, &merchURL)
	if err != nil {
		return nil, toAppErr(err, "failed to get series", slog.String("series_id", id))
	}
	if err := assignSeriesType(&s, seriesT, id); err != nil {
		return nil, err
	}
	if sourceURL != nil {
		s.SourceURL = *sourceURL
	}
	if merchURL != nil {
		s.MerchURL = *merchURL
	}
	return &s, nil
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
		var (
			s         entity.Series
			seriesT   string
			sourceURL *string
			merchURL  *string
		)
		if err := rows.Scan(&s.ID, &s.Title, &seriesT, &sourceURL, &merchURL); err != nil {
			return nil, toAppErr(err, "failed to scan series")
		}
		if err := assignSeriesType(&s, seriesT, s.ID); err != nil {
			return nil, err
		}
		if sourceURL != nil {
			s.SourceURL = *sourceURL
		}
		if merchURL != nil {
			s.MerchURL = *merchURL
		}
		result = append(result, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "series list iteration ended with error",
			slog.Int("count", len(ids)),
		)
	}
	return result, nil
}

// ListSeriesInMerchWindow returns every series whose earliest event's local
// date falls within [today, today+window], each paired with a representative
// performing artist name. The caller partitions the result on MerchURL.
func (r *SeriesRepository) ListSeriesInMerchWindow(ctx context.Context, window time.Duration) ([]*entity.MerchCandidate, error) {
	if window <= 0 {
		return nil, apperr.New(codes.InvalidArgument, "merch discovery window must be positive")
	}
	// make_interval(days => N) takes a whole number of days; round up so a
	// fractional window never silently truncates the final day out of range.
	windowDays := int(window.Hours()/24 + 0.999999)

	rows, err := r.db.Pool.Query(ctx, listSeriesInMerchWindowQuery, windowDays)
	if err != nil {
		return nil, toAppErr(err, "failed to list series in merch window", slog.Int("window_days", windowDays))
	}
	defer rows.Close()

	var result []*entity.MerchCandidate
	for rows.Next() {
		var c entity.MerchCandidate
		if err := rows.Scan(&c.SeriesID, &c.SeriesTitle, &c.MerchURL, &c.ArtistName); err != nil {
			return nil, toAppErr(err, "failed to scan merch candidate")
		}
		result = append(result, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "merch candidate iteration ended with error")
	}
	return result, nil
}

// SetMerchURL persists a resolved merch URL only when the row's merch_url is
// currently NULL (fill-once at the SQL layer). A no-op update (URL already
// present) is not an error: the dead-link/empty precondition is the caller's
// responsibility, and the WHERE clause is the backstop that guarantees a live
// link is never clobbered.
func (r *SeriesRepository) SetMerchURL(ctx context.Context, seriesID, merchURL string) error {
	if seriesID == "" {
		return apperr.New(codes.InvalidArgument, "series ID must not be empty")
	}
	if merchURL == "" {
		return apperr.New(codes.InvalidArgument, "merch URL must not be empty")
	}
	if _, err := r.db.Pool.Exec(ctx, setMerchURLQuery, seriesID, merchURL); err != nil {
		return toAppErr(err, "failed to set merch URL", slog.String("series_id", seriesID))
	}
	return nil
}

// ClearMerchURL resets a series' merch_url to NULL ahead of a re-search.
func (r *SeriesRepository) ClearMerchURL(ctx context.Context, seriesID string) error {
	if seriesID == "" {
		return apperr.New(codes.InvalidArgument, "series ID must not be empty")
	}
	if _, err := r.db.Pool.Exec(ctx, clearMerchURLQuery, seriesID); err != nil {
		return toAppErr(err, "failed to clear merch URL", slog.String("series_id", seriesID))
	}
	return nil
}
