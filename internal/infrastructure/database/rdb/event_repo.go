package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// EventEntryRepository implements entity.EventRepository for the entry system.
// This is separate from the concert-related event repository and focuses on
// entry-specific operations (merkle root, ticket leaf index).
type EventEntryRepository struct {
	db *Database
}

// NewEventEntryRepository creates a new event entry repository instance.
func NewEventEntryRepository(db *Database) *EventEntryRepository {
	return &EventEntryRepository{db: db}
}

// Compile-time interface compliance check.
var _ entity.EventRepository = (*EventEntryRepository)(nil)

const (
	getMerkleRootQuery = `
		SELECT merkle_root FROM events WHERE id = $1
	`

	updateMerkleRootQuery = `
		UPDATE events SET merkle_root = $2 WHERE id = $1
	`

	// getTicketLeafIndexQuery returns the 0-based position of a user's ticket
	// within the ordered set of tickets for an event. This position serves as
	// the leaf index in the Merkle tree.
	getTicketLeafIndexQuery = `
		SELECT idx FROM (
			SELECT id, user_id,
				ROW_NUMBER() OVER (ORDER BY minted_at ASC, id ASC) - 1 AS idx
			FROM tickets
			WHERE event_id = $1
		) t
		WHERE t.user_id = $2
		LIMIT 1
	`
)

// GetMerkleRoot retrieves the Merkle root for an event.
func (r *EventEntryRepository) GetMerkleRoot(ctx context.Context, eventID string) ([]byte, error) {
	if eventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	var root *[]byte
	err := r.db.Pool.QueryRow(ctx, getMerkleRootQuery, eventID).Scan(&root)
	if err != nil {
		return nil, toAppErr(err, "failed to get merkle root",
			slog.String("event_id", eventID),
		)
	}

	if root == nil {
		return nil, apperr.New(codes.NotFound, "merkle root not set for event")
	}

	return *root, nil
}

// UpdateMerkleRoot sets the Merkle root for an event.
func (r *EventEntryRepository) UpdateMerkleRoot(ctx context.Context, eventID string, root []byte) error {
	if eventID == "" {
		return apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	tag, err := r.db.Pool.Exec(ctx, updateMerkleRootQuery, eventID, root)
	if err != nil {
		return toAppErr(err, "failed to update merkle root",
			slog.String("event_id", eventID),
		)
	}

	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "event not found")
	}

	return nil
}

// GetTicketLeafIndex returns the leaf index in the Merkle tree for a user's ticket.
// Returns -1 if the user has no ticket for the event.
func (r *EventEntryRepository) GetTicketLeafIndex(ctx context.Context, eventID, userID string) (int, error) {
	if eventID == "" {
		return -1, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	if userID == "" {
		return -1, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	var idx int
	err := r.db.Pool.QueryRow(ctx, getTicketLeafIndexQuery, eventID, userID).Scan(&idx)
	if err != nil {
		return -1, toAppErr(err, "failed to get ticket leaf index",
			slog.String("event_id", eventID),
			slog.String("user_id", userID),
		)
	}

	return idx, nil
}
