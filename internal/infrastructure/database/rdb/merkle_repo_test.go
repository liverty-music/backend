package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedMerkleTestData inserts a user, venue, and event needed by the merkle tree FK constraints.
// Returns eventID.
func seedMerkleTestData(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	var venueID string
	err := testDB.Pool.QueryRow(ctx,
		`INSERT INTO venues (name, raw_name) VALUES ($1, $2) RETURNING id`,
		"merkle-test-venue", "merkle-test-venue",
	).Scan(&venueID)
	require.NoError(t, err)

	var eventID string
	err = testDB.Pool.QueryRow(ctx,
		`INSERT INTO events (venue_id, title, local_event_date) VALUES ($1, $2, $3) RETURNING id`,
		venueID, "merkle-test-event", "2026-03-01",
	).Scan(&eventID)
	require.NoError(t, err)

	return eventID
}

func TestMerkleTreeRepository_StoreBatch(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewMerkleTreeRepository(testDB)
	ctx := context.Background()
	eventID := seedMerkleTestData(t)

	t.Run("store nodes successfully", func(t *testing.T) {
		nodes := []*entity.MerkleNode{
			{EventID: eventID, Depth: 0, NodeIndex: 0, Hash: []byte("leaf0")},
			{EventID: eventID, Depth: 0, NodeIndex: 1, Hash: []byte("leaf1")},
			{EventID: eventID, Depth: 1, NodeIndex: 0, Hash: []byte("root")},
		}

		err := repo.StoreBatch(ctx, eventID, nodes)
		require.NoError(t, err)

		// Verify nodes were stored by reading them back.
		root, err := repo.GetRoot(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, []byte("root"), root)
	})

	t.Run("replace existing nodes on second call", func(t *testing.T) {
		// First store.
		nodes1 := []*entity.MerkleNode{
			{EventID: eventID, Depth: 0, NodeIndex: 0, Hash: []byte("old-leaf0")},
			{EventID: eventID, Depth: 1, NodeIndex: 0, Hash: []byte("old-root")},
		}
		err := repo.StoreBatch(ctx, eventID, nodes1)
		require.NoError(t, err)

		// Second store should replace.
		nodes2 := []*entity.MerkleNode{
			{EventID: eventID, Depth: 0, NodeIndex: 0, Hash: []byte("new-leaf0")},
			{EventID: eventID, Depth: 0, NodeIndex: 1, Hash: []byte("new-leaf1")},
			{EventID: eventID, Depth: 1, NodeIndex: 0, Hash: []byte("new-root")},
		}
		err = repo.StoreBatch(ctx, eventID, nodes2)
		require.NoError(t, err)

		root, err := repo.GetRoot(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, []byte("new-root"), root)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		err := repo.StoreBatch(ctx, "", nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestMerkleTreeRepository_StoreBatchWithRoot(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewMerkleTreeRepository(testDB)
	eventRepo := rdb.NewEventEntryRepository(testDB)
	ctx := context.Background()
	eventID := seedMerkleTestData(t)

	t.Run("store nodes and update merkle root atomically", func(t *testing.T) {
		rootHash := []byte("atomic-root-hash-32-bytes-padded!")
		nodes := []*entity.MerkleNode{
			{EventID: eventID, Depth: 0, NodeIndex: 0, Hash: []byte("leaf0")},
			{EventID: eventID, Depth: 0, NodeIndex: 1, Hash: []byte("leaf1")},
			{EventID: eventID, Depth: 1, NodeIndex: 0, Hash: rootHash},
		}

		err := repo.StoreBatchWithRoot(ctx, eventID, nodes, rootHash)
		require.NoError(t, err)

		// Verify the merkle root was updated on the event.
		gotRoot, err := eventRepo.GetMerkleRoot(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, rootHash, gotRoot)

		// Verify nodes were stored.
		treeRoot, err := repo.GetRoot(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, rootHash, treeRoot)
	})

	t.Run("non-existent event returns FailedPrecondition", func(t *testing.T) {
		nodes := []*entity.MerkleNode{
			{EventID: "018b2f19-e591-7d12-bf9e-000000000000", Depth: 0, NodeIndex: 0, Hash: []byte("leaf")},
		}
		err := repo.StoreBatchWithRoot(ctx, "018b2f19-e591-7d12-bf9e-000000000000", nodes, []byte("root"))
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		err := repo.StoreBatchWithRoot(ctx, "", nil, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestMerkleTreeRepository_GetPath(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewMerkleTreeRepository(testDB)
	ctx := context.Background()
	eventID := seedMerkleTestData(t)

	// Build a simple depth-2 tree with 4 leaves:
	//        root (depth=2, index=0)
	//       /    \
	//   n10       n11    (depth=1)
	//  /   \     /   \
	// L0   L1   L2   L3  (depth=0)
	nodes := []*entity.MerkleNode{
		{EventID: eventID, Depth: 0, NodeIndex: 0, Hash: []byte("L0-hash")},
		{EventID: eventID, Depth: 0, NodeIndex: 1, Hash: []byte("L1-hash")},
		{EventID: eventID, Depth: 0, NodeIndex: 2, Hash: []byte("L2-hash")},
		{EventID: eventID, Depth: 0, NodeIndex: 3, Hash: []byte("L3-hash")},
		{EventID: eventID, Depth: 1, NodeIndex: 0, Hash: []byte("N10-hash")},
		{EventID: eventID, Depth: 1, NodeIndex: 1, Hash: []byte("N11-hash")},
		{EventID: eventID, Depth: 2, NodeIndex: 0, Hash: []byte("root-hash")},
	}
	err := repo.StoreBatch(ctx, eventID, nodes)
	require.NoError(t, err)

	t.Run("path for leaf 0 (left child at every level)", func(t *testing.T) {
		pathElements, pathIndices, err := repo.GetPath(ctx, eventID, 0, 2)
		require.NoError(t, err)
		require.Len(t, pathElements, 2)
		require.Len(t, pathIndices, 2)

		// Leaf 0: sibling at depth 0 is index 0^1=1 (L1)
		assert.Equal(t, []byte("L1-hash"), pathElements[0])
		assert.Equal(t, uint32(0), pathIndices[0]) // leaf 0 is left child (even index)

		// Parent index = 0/2 = 0; sibling at depth 1 is index 0^1=1 (N11)
		assert.Equal(t, []byte("N11-hash"), pathElements[1])
		assert.Equal(t, uint32(0), pathIndices[1]) // parent 0 is left child
	})

	t.Run("path for leaf 3 (right child at every level)", func(t *testing.T) {
		pathElements, pathIndices, err := repo.GetPath(ctx, eventID, 3, 2)
		require.NoError(t, err)
		require.Len(t, pathElements, 2)

		// Leaf 3: sibling at depth 0 is index 3^1=2 (L2)
		assert.Equal(t, []byte("L2-hash"), pathElements[0])
		assert.Equal(t, uint32(1), pathIndices[0]) // leaf 3 is right child (odd index)

		// Parent index = 3/2 = 1; sibling at depth 1 is index 1^1=0 (N10)
		assert.Equal(t, []byte("N10-hash"), pathElements[1])
		assert.Equal(t, uint32(1), pathIndices[1]) // parent 1 is right child
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		_, _, err := repo.GetPath(ctx, "", 0, 2)
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestMerkleTreeRepository_GetRoot(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewMerkleTreeRepository(testDB)
	ctx := context.Background()
	eventID := seedMerkleTestData(t)

	t.Run("get root from stored tree", func(t *testing.T) {
		nodes := []*entity.MerkleNode{
			{EventID: eventID, Depth: 0, NodeIndex: 0, Hash: []byte("leaf")},
			{EventID: eventID, Depth: 1, NodeIndex: 0, Hash: []byte("the-root")},
		}
		err := repo.StoreBatch(ctx, eventID, nodes)
		require.NoError(t, err)

		root, err := repo.GetRoot(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, []byte("the-root"), root)
	})

	t.Run("non-existent event returns NotFound", func(t *testing.T) {
		_, err := repo.GetRoot(ctx, "018b2f19-e591-7d12-bf9e-000000000000")
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		_, err := repo.GetRoot(ctx, "")
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestMerkleTreeRepository_GetLeaf(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewMerkleTreeRepository(testDB)
	ctx := context.Background()
	eventID := seedMerkleTestData(t)

	nodes := []*entity.MerkleNode{
		{EventID: eventID, Depth: 0, NodeIndex: 0, Hash: []byte("leaf-zero")},
		{EventID: eventID, Depth: 0, NodeIndex: 1, Hash: []byte("leaf-one")},
		{EventID: eventID, Depth: 1, NodeIndex: 0, Hash: []byte("root")},
	}
	err := repo.StoreBatch(ctx, eventID, nodes)
	require.NoError(t, err)

	t.Run("get existing leaf", func(t *testing.T) {
		leaf, err := repo.GetLeaf(ctx, eventID, 0)
		require.NoError(t, err)
		assert.Equal(t, []byte("leaf-zero"), leaf)

		leaf, err = repo.GetLeaf(ctx, eventID, 1)
		require.NoError(t, err)
		assert.Equal(t, []byte("leaf-one"), leaf)
	})

	t.Run("non-existent leaf index returns NotFound", func(t *testing.T) {
		_, err := repo.GetLeaf(ctx, eventID, 99)
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		_, err := repo.GetLeaf(ctx, "", 0)
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
