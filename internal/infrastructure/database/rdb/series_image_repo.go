package rdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// SeriesImageRepository implements entity.SeriesImageRepository over the
// series_images and series_cover_images tables.
type SeriesImageRepository struct {
	db *Database
}

var _ entity.SeriesImageRepository = (*SeriesImageRepository)(nil)

// NewSeriesImageRepository creates a SeriesImageRepository.
func NewSeriesImageRepository(db *Database) *SeriesImageRepository {
	return &SeriesImageRepository{db: db}
}

// Create persists a series image row idempotently. ON CONFLICT(id) DO NOTHING
// means a second SetCoverImage call for the same series_image_id is a safe no-op.
func (r *SeriesImageRepository) Create(ctx context.Context, seriesImage *entity.SeriesImage) error {
	attrsJSON, err := json.Marshal(seriesImage.Attributes)
	if err != nil {
		return apperr.New(codes.Internal, fmt.Sprintf("marshal image attributes: %v", err))
	}
	if _, err := r.db.Pool.Exec(ctx, insertSeriesImageIdempotentQuery,
		seriesImage.ID, seriesImage.OrganizerID, attrsJSON,
	); err != nil {
		return toAppErr(err, "failed to insert series image row", slog.String("series_image_id", seriesImage.ID))
	}
	return nil
}

// Get retrieves a single series image row by id. Returns NotFound when no
// row exists.
func (r *SeriesImageRepository) Get(ctx context.Context, seriesImageID string) (*entity.SeriesImage, error) {
	var (
		m        entity.SeriesImage
		attrsRaw []byte
	)
	err := r.db.Pool.QueryRow(ctx, getSeriesImageQuery, seriesImageID).Scan(
		&m.ID, &m.OrganizerID, &attrsRaw,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to find series image by id", slog.String("series_image_id", seriesImageID))
	}
	if len(attrsRaw) > 0 {
		if err := json.Unmarshal(attrsRaw, &m.Attributes); err != nil {
			return nil, apperr.New(codes.Internal, fmt.Sprintf("unmarshal image attributes: %v", err))
		}
	}
	return &m, nil
}

// ReplaceCover atomically re-points a series' cover to newSeriesImageID and
// returns the prior series image id (empty string when the series had no cover or
// when series_cover_images already points to newSeriesImageID — idempotent on re-delivery).
// The old series image row is deleted inside the same transaction so the DB stays
// consistent even when the caller crashes before reclaiming GCS objects.
func (r *SeriesImageRepository) ReplaceCover(ctx context.Context, seriesID, newSeriesImageID string) (string, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return "", toAppErr(err, "failed to begin cut-over transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read the current series_cover_images row while holding the transaction lock.
	var oldSeriesImageID sql.NullString
	if err := tx.QueryRow(ctx, selectCoverImageIDQuery, seriesID).Scan(&oldSeriesImageID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", toAppErr(err, "failed to read current cover image", slog.String("series_id", seriesID))
	}

	// Idempotency: already pointing to the new id — nothing to do.
	if oldSeriesImageID.Valid && oldSeriesImageID.String == newSeriesImageID {
		if err := tx.Commit(ctx); err != nil {
			return "", toAppErr(err, "failed to commit no-op cut-over transaction")
		}
		return "", nil
	}

	// Upsert series_cover_images to point to the new series image id.
	if _, err := tx.Exec(ctx, deleteSeriesCoverImageBySeriesQuery, seriesID); err != nil {
		return "", toAppErr(err, "failed to delete old series_cover_images row", slog.String("series_id", seriesID))
	}
	if _, err := tx.Exec(ctx, insertSeriesCoverImageQuery, seriesID, newSeriesImageID); err != nil {
		if IsForeignKeyViolation(err) {
			return "", apperr.New(codes.NotFound, "series not found")
		}
		return "", toAppErr(err, "failed to insert new series_cover_images row",
			slog.String("series_id", seriesID),
			slog.String("new_series_image_id", newSeriesImageID),
		)
	}

	// Delete the old series image row so the DB does not accumulate orphaned rows.
	if oldSeriesImageID.Valid && oldSeriesImageID.String != "" {
		if _, err := tx.Exec(ctx, deleteCoverImageRowQuery, oldSeriesImageID.String); err != nil {
			return "", toAppErr(err, "failed to delete old series image row",
				slog.String("series_id", seriesID),
				slog.String("old_series_image_id", oldSeriesImageID.String),
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", toAppErr(err, "failed to commit cut-over transaction")
	}

	old := ""
	if oldSeriesImageID.Valid {
		old = oldSeriesImageID.String
	}
	return old, nil
}

// Delete removes a series image row by id. Missing rows are silently skipped so
// the operation is idempotent.
func (r *SeriesImageRepository) Delete(ctx context.Context, seriesImageID string) error {
	if _, err := r.db.Pool.Exec(ctx, deleteSeriesImageQuery, seriesImageID); err != nil {
		return toAppErr(err, "failed to delete series image row", slog.String("series_image_id", seriesImageID))
	}
	return nil
}

// ListByOrganizer returns every series image row owned by the organizer, ordered by id
// (creation order, as ids are UUIDv7). Returns an empty slice when it owns none.
func (r *SeriesImageRepository) ListByOrganizer(ctx context.Context, organizerID string) ([]*entity.SeriesImage, error) {
	rows, err := r.db.Pool.Query(ctx, listSeriesImagesByOrganizerQuery, organizerID)
	if err != nil {
		return nil, toAppErr(err, "failed to list series images by organizer", slog.String("organizer_id", organizerID))
	}
	defer rows.Close()

	seriesImage := []*entity.SeriesImage{}
	for rows.Next() {
		var (
			m        entity.SeriesImage
			attrsRaw []byte
		)
		if err := rows.Scan(&m.ID, &m.OrganizerID, &attrsRaw); err != nil {
			return nil, toAppErr(err, "failed to scan series images", slog.String("organizer_id", organizerID))
		}
		if len(attrsRaw) > 0 {
			if err := json.Unmarshal(attrsRaw, &m.Attributes); err != nil {
				return nil, apperr.New(codes.Internal, fmt.Sprintf("unmarshal image attributes: %v", err))
			}
		}
		seriesImage = append(seriesImage, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate series images", slog.String("organizer_id", organizerID))
	}
	return seriesImage, nil
}
