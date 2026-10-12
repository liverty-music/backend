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

// Create persists a media row idempotently. ON CONFLICT(id) DO NOTHING
// means a second SetCoverImage call for the same media_id is a safe no-op.
func (r *SeriesImageRepository) Create(ctx context.Context, media *entity.SeriesImage) error {
	attrsJSON, err := json.Marshal(media.Attributes)
	if err != nil {
		return apperr.New(codes.Internal, fmt.Sprintf("marshal media attributes: %v", err))
	}
	if _, err := r.db.Pool.Exec(ctx, insertMediaIdempotentQuery,
		media.ID, media.OrganizerID, attrsJSON,
	); err != nil {
		return toAppErr(err, "failed to insert media row", slog.String("media_id", media.ID))
	}
	return nil
}

// Get retrieves a single media row by id. Returns NotFound when no
// row exists.
func (r *SeriesImageRepository) Get(ctx context.Context, mediaID string) (*entity.SeriesImage, error) {
	var (
		m        entity.SeriesImage
		attrsRaw []byte
	)
	err := r.db.Pool.QueryRow(ctx, findMediaByIDQuery, mediaID).Scan(
		&m.ID, &m.OrganizerID, &attrsRaw,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to find media by id", slog.String("media_id", mediaID))
	}
	if len(attrsRaw) > 0 {
		if err := json.Unmarshal(attrsRaw, &m.Attributes); err != nil {
			return nil, apperr.New(codes.Internal, fmt.Sprintf("unmarshal media attributes: %v", err))
		}
	}
	return &m, nil
}

// ReplaceCover atomically re-points a series' cover to newMediaID and
// returns the prior media id (empty string when the series had no cover or
// when series_cover_images already points to newMediaID — idempotent on re-delivery).
// The old media row is deleted inside the same transaction so the DB stays
// consistent even when the caller crashes before reclaiming GCS objects.
func (r *SeriesImageRepository) ReplaceCover(ctx context.Context, seriesID, newMediaID string) (string, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return "", toAppErr(err, "failed to begin cut-over transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read the current series_cover_images row while holding the transaction lock.
	var oldMediaID sql.NullString
	if err := tx.QueryRow(ctx, selectCoverImageIDQuery, seriesID).Scan(&oldMediaID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", toAppErr(err, "failed to read current cover media", slog.String("series_id", seriesID))
	}

	// Idempotency: already pointing to the new id — nothing to do.
	if oldMediaID.Valid && oldMediaID.String == newMediaID {
		if err := tx.Commit(ctx); err != nil {
			return "", toAppErr(err, "failed to commit no-op cut-over transaction")
		}
		return "", nil
	}

	// Upsert series_cover_images to point to the new media id.
	if _, err := tx.Exec(ctx, deleteSeriesMediaBySeriesQuery, seriesID); err != nil {
		return "", toAppErr(err, "failed to delete old series_media row", slog.String("series_id", seriesID))
	}
	if _, err := tx.Exec(ctx, insertSeriesMediaQuery, seriesID, newMediaID); err != nil {
		if IsForeignKeyViolation(err) {
			return "", apperr.New(codes.NotFound, "series not found")
		}
		return "", toAppErr(err, "failed to insert new series_media row",
			slog.String("series_id", seriesID),
			slog.String("new_media_id", newMediaID),
		)
	}

	// Delete the old media row so the DB does not accumulate orphaned rows.
	if oldMediaID.Valid && oldMediaID.String != "" {
		if _, err := tx.Exec(ctx, deleteCoverImageRowQuery, oldMediaID.String); err != nil {
			return "", toAppErr(err, "failed to delete old media row",
				slog.String("series_id", seriesID),
				slog.String("old_media_id", oldMediaID.String),
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", toAppErr(err, "failed to commit cut-over transaction")
	}

	old := ""
	if oldMediaID.Valid {
		old = oldMediaID.String
	}
	return old, nil
}

// Delete removes a media row by id. Missing rows are silently skipped so
// the operation is idempotent.
func (r *SeriesImageRepository) Delete(ctx context.Context, mediaID string) error {
	if _, err := r.db.Pool.Exec(ctx, deleteMediaByIDQuery, mediaID); err != nil {
		return toAppErr(err, "failed to delete media row", slog.String("media_id", mediaID))
	}
	return nil
}

// ListByOrganizer returns every media row owned by the organizer, ordered by id
// (creation order, as ids are UUIDv7). Returns an empty slice when it owns none.
func (r *SeriesImageRepository) ListByOrganizer(ctx context.Context, organizerID string) ([]*entity.SeriesImage, error) {
	rows, err := r.db.Pool.Query(ctx, listMediaByOrganizerQuery, organizerID)
	if err != nil {
		return nil, toAppErr(err, "failed to list media by organizer", slog.String("organizer_id", organizerID))
	}
	defer rows.Close()

	media := []*entity.SeriesImage{}
	for rows.Next() {
		var (
			m        entity.SeriesImage
			attrsRaw []byte
		)
		if err := rows.Scan(&m.ID, &m.OrganizerID, &attrsRaw); err != nil {
			return nil, toAppErr(err, "failed to scan media", slog.String("organizer_id", organizerID))
		}
		if len(attrsRaw) > 0 {
			if err := json.Unmarshal(attrsRaw, &m.Attributes); err != nil {
				return nil, apperr.New(codes.Internal, fmt.Sprintf("unmarshal media attributes: %v", err))
			}
		}
		media = append(media, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate media", slog.String("organizer_id", organizerID))
	}
	return media, nil
}
