package rdb

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
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

	// getSiblingsQuery fetches all sibling nodes for a Merkle path in a
	// single round trip. $1 = event_id, $2 = depths array, $3 = sibling
	// indices array (parallel arrays, one element per tree level).
	getSiblingsQuery = `
		SELECT mt.hash
		FROM unnest($2::int[], $3::int[]) AS params(depth, node_index)
		JOIN merkle_tree mt
		  ON mt.event_id = $1 AND mt.depth = params.depth AND mt.node_index = params.node_index
		ORDER BY params.depth
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
// Node inserts are pipelined via pgx.SendBatch for a single round trip.
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

	// Pipeline all inserts in a single batch round trip.
	if err := r.batchInsertNodes(ctx, tx, eventID, nodes); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit merkle tree store",
			slog.String("event_id", eventID),
		)
	}

	return nil
}

// StoreBatchWithRoot atomically stores all Merkle tree nodes and updates
// the event's Merkle root in a single database transaction.
func (r *MerkleTreeRepository) StoreBatchWithRoot(ctx context.Context, eventID string, nodes []*entity.MerkleNode, root []byte) error {
	if eventID == "" {
		return apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction for merkle tree store with root")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Delete existing nodes for this event.
	if _, err := tx.Exec(ctx, deleteMerkleNodesQuery, eventID); err != nil {
		return toAppErr(err, "failed to delete existing merkle nodes",
			slog.String("event_id", eventID),
		)
	}

	// Pipeline all inserts in a single batch round trip.
	if err := r.batchInsertNodes(ctx, tx, eventID, nodes); err != nil {
		return err
	}

	// Update the event's Merkle root within the same transaction.
	tag, err := tx.Exec(ctx, updateMerkleRootQuery, eventID, root)
	if err != nil {
		return toAppErr(err, "failed to update merkle root",
			slog.String("event_id", eventID),
		)
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "event not found")
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit merkle tree store with root",
			slog.String("event_id", eventID),
		)
	}

	return nil
}

// batchInsertNodes pipelines all node inserts via pgx.SendBatch for a single
// round trip instead of one Exec per node.
func (r *MerkleTreeRepository) batchInsertNodes(ctx context.Context, tx pgx.Tx, eventID string, nodes []*entity.MerkleNode) error {
	if len(nodes) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, node := range nodes {
		batch.Queue(insertMerkleNodeQuery, node.EventID, node.Depth, node.NodeIndex, node.Hash)
	}

	br := tx.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for i, node := range nodes {
		if _, err := br.Exec(); err != nil {
			return toAppErr(err, "failed to insert merkle node",
				slog.String("event_id", eventID),
				slog.Int("batch_index", i),
				slog.Int("depth", node.Depth),
				slog.Int("node_index", node.NodeIndex),
			)
		}
	}

	return nil
}

// GetPath retrieves the Merkle path for a leaf at the given index.
// Returns path elements (sibling hashes) and path indices (0=left, 1=right).
//
// All sibling hashes are fetched in a single query rather than one per depth
// level, reducing database round trips from O(treeDepth) to O(1).
func (r *MerkleTreeRepository) GetPath(ctx context.Context, eventID string, leafIndex int, treeDepth int) ([][]byte, []uint32, error) {
	if eventID == "" {
		return nil, nil, apperr.New(codes.InvalidArgument, "event ID cannot be empty")
	}

	// Precompute sibling indices and path directions for every depth level.
	depths := make([]int, treeDepth)
	siblingIndices := make([]int, treeDepth)
	pathIndices := make([]uint32, treeDepth)

	currentIndex := leafIndex
	for depth := range treeDepth {
		depths[depth] = depth
		siblingIndices[depth] = currentIndex ^ 1

		if currentIndex%2 == 0 {
			pathIndices[depth] = 0
		} else {
			pathIndices[depth] = 1
		}
		currentIndex = currentIndex / 2
	}

	// Fetch all sibling hashes in a single round trip.
	rows, err := r.db.Pool.Query(ctx, getSiblingsQuery, eventID, depths, siblingIndices)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to get merkle path",
			slog.String("event_id", eventID),
			slog.Int("leaf_index", leafIndex),
		)
	}
	defer rows.Close()

	pathElements := make([][]byte, 0, treeDepth)
	for rows.Next() {
		var hash []byte
		if err := rows.Scan(&hash); err != nil {
			return nil, nil, toAppErr(err, "failed to scan merkle sibling hash",
				slog.String("event_id", eventID),
			)
		}
		pathElements = append(pathElements, hash)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, toAppErr(err, "failed to iterate merkle siblings",
			slog.String("event_id", eventID),
		)
	}

	if len(pathElements) != treeDepth {
		return nil, nil, apperr.New(codes.Internal, "merkle path incomplete: expected %d siblings, got %d",
			slog.Int("expected", treeDepth),
			slog.Int("got", len(pathElements)),
		)
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
