package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// MerkleTreeRepository implements entity.MerkleTreeRepository interface.
type MerkleTreeRepository struct {
	db *Database
}

// NewMerkleTreeRepository creates a new Merkle tree repository instance.
func NewMerkleTreeRepository(db *Database) *MerkleTreeRepository {
	return &MerkleTreeRepository{db: db}
}

// Compile-time interface compliance check.
var _ entity.MerkleTreeRepository = (*MerkleTreeRepository)(nil)

const (
	deleteMerkleNodesQuery = `DELETE FROM merkle_tree WHERE event_id = $1`

	insertMerkleNodeQuery = `
		INSERT INTO merkle_tree (event_id, depth, node_index, hash)
		VALUES ($1, $2, $3, $4)
	`

	// getSiblingQuery fetches the sibling node at a given depth and index.
	// For a node at index i, its sibling is at index i^1 (XOR with 1).
	getSiblingQuery = `
		SELECT hash FROM merkle_tree
		WHERE event_id = $1 AND depth = $2 AND node_index = $3
	`

	getRootQuery = `
		SELECT hash FROM merkle_tree
		WHERE event_id = $1 AND depth = (
			SELECT MAX(depth) FROM merkle_tree WHERE event_id = $1
		) AND node_index = 0
	`

	getLeafQuery = `
		SELECT hash FROM merkle_tree
		WHERE event_id = $1 AND depth = 0 AND node_index = $2
	`
)

// StoreBatch replaces all nodes for an event's Merkle tree within a transaction.
func (r *MerkleTreeRepository) StoreBatch(ctx context.Context, eventID string, nodes []*entity.MerkleNode) error {
	if eventID == "" {
		return apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction for merkle tree store")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Delete existing nodes for this event.
	if _, err := tx.Exec(ctx, deleteMerkleNodesQuery, eventID); err != nil {
		return toAppErr(err, "failed to delete existing merkle nodes",
			slog.String("event_id", eventID),
		)
	}

	// Insert all new nodes.
	for _, node := range nodes {
		if _, err := tx.Exec(ctx, insertMerkleNodeQuery,
			node.EventID, node.Depth, node.NodeIndex, node.Hash,
		); err != nil {
			return toAppErr(err, "failed to insert merkle node",
				slog.String("event_id", eventID),
				slog.Int("depth", node.Depth),
				slog.Int("node_index", node.NodeIndex),
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit merkle tree store",
			slog.String("event_id", eventID),
		)
	}

	return nil
}

// GetPath retrieves the Merkle path for a leaf at the given index.
// Returns path elements (sibling hashes) and path indices (0=left, 1=right).
func (r *MerkleTreeRepository) GetPath(ctx context.Context, eventID string, leafIndex int, treeDepth int) ([][]byte, []uint32, error) {
	if eventID == "" {
		return nil, nil, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	pathElements := make([][]byte, treeDepth)
	pathIndices := make([]uint32, treeDepth)

	currentIndex := leafIndex
	for depth := 0; depth < treeDepth; depth++ {
		// Determine sibling index: XOR with 1.
		siblingIndex := currentIndex ^ 1

		// Path index: 0 if current node is on the left (even index), 1 if on the right.
		if currentIndex%2 == 0 {
			pathIndices[depth] = 0
		} else {
			pathIndices[depth] = 1
		}

		var siblingHash []byte
		err := r.db.Pool.QueryRow(ctx, getSiblingQuery, eventID, depth, siblingIndex).Scan(&siblingHash)
		if err != nil {
			return nil, nil, toAppErr(err, "failed to get merkle sibling",
				slog.String("event_id", eventID),
				slog.Int("depth", depth),
				slog.Int("sibling_index", siblingIndex),
			)
		}
		pathElements[depth] = siblingHash

		// Move to parent index.
		currentIndex = currentIndex / 2
	}

	return pathElements, pathIndices, nil
}

// GetRoot retrieves the Merkle root hash for an event.
func (r *MerkleTreeRepository) GetRoot(ctx context.Context, eventID string) ([]byte, error) {
	if eventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	var root []byte
	err := r.db.Pool.QueryRow(ctx, getRootQuery, eventID).Scan(&root)
	if err != nil {
		return nil, toAppErr(err, "failed to get merkle root",
			slog.String("event_id", eventID),
		)
	}

	return root, nil
}

// GetLeaf retrieves the leaf hash at the given index for an event.
func (r *MerkleTreeRepository) GetLeaf(ctx context.Context, eventID string, leafIndex int) ([]byte, error) {
	if eventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	var leaf []byte
	err := r.db.Pool.QueryRow(ctx, getLeafQuery, eventID, leafIndex).Scan(&leaf)
	if err != nil {
		return nil, toAppErr(err, "failed to get merkle leaf",
			slog.String("event_id", eventID),
			slog.Int("leaf_index", leafIndex),
		)
	}

	return leaf, nil
}
