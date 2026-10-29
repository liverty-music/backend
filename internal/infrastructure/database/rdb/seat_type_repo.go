package rdb

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// SeatTypeRepository implements [entity.SeatTypeRepository] for PostgreSQL.
type SeatTypeRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.SeatTypeRepository = (*SeatTypeRepository)(nil)

// NewSeatTypeRepository creates a new SeatTypeRepository.
func NewSeatTypeRepository(db *Database) *SeatTypeRepository {
	return &SeatTypeRepository{db: db}
}

const (
	seatTypeColumns = `id, series_id, name, price`
	// seatTypeInsertQuery stores the seat type only for a first-party Series:
	// a missing or discovered Series inserts no row.
	seatTypeInsertQuery = `
		INSERT INTO seat_types (id, series_id, name, price)
		SELECT $1, s.id, $3, $4 FROM series s WHERE s.id = $2 AND s.organizer_id IS NOT NULL
	`
	seatTypeGetQuery          = `SELECT ` + seatTypeColumns + ` FROM seat_types WHERE id = $1`
	seatTypeDeleteQuery       = `DELETE FROM seat_types WHERE id = $1`
	seatTypeListBySeriesQuery = `SELECT ` + seatTypeColumns + ` FROM seat_types WHERE series_id = $1 ORDER BY price DESC, name, id`
)

// Create stores the seat type of a first-party Series.
func (r *SeatTypeRepository) Create(ctx context.Context, seatType *entity.SeatType) (*entity.SeatType, error) {
	attrs := []slog.Attr{slog.String("series_id", seatType.SeriesID), slog.String("seat_type_id", string(seatType.ID))}
	if err := seatType.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error(), attrs...)
	}
	tag, err := r.db.Pool.Exec(ctx, seatTypeInsertQuery, string(seatType.ID), seatType.SeriesID, seatType.Name, seatType.Price)
	if err != nil {
		return nil, toAppErr(err, "failed to create seat type", attrs...)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperr.New(codes.FailedPrecondition, "the series does not exist or is not first-party", attrs...)
	}
	return seatType, nil
}

// Get returns the seat type with the given id.
func (r *SeatTypeRepository) Get(ctx context.Context, id entity.SeatTypeID) (*entity.SeatType, error) {
	s, err := scanSeatType(r.db.Pool.QueryRow(ctx, seatTypeGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get seat type", slog.String("seat_type_id", string(id)))
	}
	return s, nil
}

// Delete removes the seat type; the ticket_stocks foreign key refuses it while
// a stock uses it, also one inserted while the delete runs.
func (r *SeatTypeRepository) Delete(ctx context.Context, id entity.SeatTypeID) error {
	attrs := slog.String("seat_type_id", string(id))
	tag, err := r.db.Pool.Exec(ctx, seatTypeDeleteQuery, string(id))
	if err != nil {
		return toAppErr(err, "failed to delete seat type", attrs)
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "no seat type has the id", attrs)
	}
	return nil
}

// ListBySeries returns the Series' seat types, highest price first, then by
// name.
func (r *SeatTypeRepository) ListBySeries(ctx context.Context, seriesID string) ([]*entity.SeatType, error) {
	attrs := slog.String("series_id", seriesID)
	rows, err := r.db.Pool.Query(ctx, seatTypeListBySeriesQuery, seriesID)
	if err != nil {
		return nil, toAppErr(err, "failed to list seat types", attrs)
	}
	types, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.SeatType, error) { return scanSeatType(row) })
	if err != nil {
		return nil, toAppErr(err, "failed to scan seat types", attrs)
	}
	return types, nil
}

// scanSeatType maps one row selected with seatTypeColumns.
func scanSeatType(row pgx.Row) (*entity.SeatType, error) {
	var (
		s  entity.SeatType
		id string
	)
	if err := row.Scan(&id, &s.SeriesID, &s.Name, &s.Price); err != nil {
		return nil, err
	}
	s.ID = entity.SeatTypeID(id)
	return &s, nil
}
