package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// NullifierRepository implements entity.NullifierRepository interface.
type NullifierRepository struct {
	db *Database
}

// NewNullifierRepository creates a new nullifier repository instance.
func NewNullifierRepository(db *Database) *NullifierRepository {
	return &NullifierRepository{db: db}
}

// Compile-time interface compliance check.
var _ entity.NullifierRepository = (*NullifierRepository)(nil)

const (
	insertNullifierQuery = `
		INSERT INTO nullifiers (event_id, nullifier_hash)
		VALUES ($1, $2)
	`

	nullifierExistsQuery = `
		SELECT EXISTS(
			SELECT 1 FROM nullifiers
			WHERE event_id = $1 AND nullifier_hash = $2
		)
	`
)

// Insert atomically inserts a nullifier hash for an event.
// Returns AlreadyExists if the nullifier has already been used.
func (r *NullifierRepository) Insert(ctx context.Context, eventID string, nullifierHash []byte) error {
	if eventID == "" {
		return apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	if len(nullifierHash) == 0 {
		return apperr.New(codes.InvalidArgument, "nullifier hash cannot be empty")
	}

	_, err := r.db.Pool.Exec(ctx, insertNullifierQuery, eventID, nullifierHash)
	if err != nil {
		return toAppErr(err, "failed to insert nullifier",
			slog.String("event_id", eventID),
		)
	}

	return nil
}

// Exists checks if a nullifier hash has already been used for an event.
func (r *NullifierRepository) Exists(ctx context.Context, eventID string, nullifierHash []byte) (bool, error) {
	if eventID == "" {
		return false, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	if len(nullifierHash) == 0 {
		return false, apperr.New(codes.InvalidArgument, "nullifier hash cannot be empty")
	}

	var exists bool
	err := r.db.Pool.QueryRow(ctx, nullifierExistsQuery, eventID, nullifierHash).Scan(&exists)
	if err != nil {
		return false, toAppErr(err, "failed to check nullifier existence",
			slog.String("event_id", eventID),
		)
	}

	return exists, nil
}
