package rdb

import (
	"context"
	"log/slog"

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

	getSeriesQuery = `
		SELECT id, title, type, source_url
		FROM series
		WHERE id = $1
	`

	listSeriesByIDsQuery = `
		SELECT id, title, type, source_url
		FROM series
		WHERE id = ANY($1)
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
	)
	err := r.db.Pool.QueryRow(ctx, getSeriesQuery, id).Scan(&s.ID, &s.Title, &seriesT, &sourceURL)
	if err != nil {
		return nil, toAppErr(err, "failed to get series", slog.String("series_id", id))
	}
	if err := assignSeriesType(&s, seriesT, id); err != nil {
		return nil, err
	}
	if sourceURL != nil {
		s.SourceURL = *sourceURL
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
		)
		if err := rows.Scan(&s.ID, &s.Title, &seriesT, &sourceURL); err != nil {
			return nil, toAppErr(err, "failed to scan series")
		}
		if err := assignSeriesType(&s, seriesT, s.ID); err != nil {
			return nil, err
		}
		if sourceURL != nil {
			s.SourceURL = *sourceURL
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
