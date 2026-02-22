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

func TestEventEntryRepository_GetMerkleRoot(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewEventEntryRepository(testDB)
	ctx := context.Background()
	eventID := seedMerkleTestData(t)

	t.Run("returns NotFound when merkle root is NULL", func(t *testing.T) {
		// Event was just created with NULL merkle_root.
		_, err := repo.GetMerkleRoot(ctx, eventID)
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("returns root after update", func(t *testing.T) {
		root := []byte("merkle-root-32-bytes-of-data!!")

		err := repo.UpdateMerkleRoot(ctx, eventID, root)
		require.NoError(t, err)

		got, err := repo.GetMerkleRoot(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, root, got)
	})

	t.Run("non-existent event returns NotFound", func(t *testing.T) {
		_, err := repo.GetMerkleRoot(ctx, "018b2f19-e591-7d12-bf9e-000000000000")
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		_, err := repo.GetMerkleRoot(ctx, "")
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestEventEntryRepository_UpdateMerkleRoot(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewEventEntryRepository(testDB)
	ctx := context.Background()
	eventID := seedMerkleTestData(t)

	t.Run("update merkle root successfully", func(t *testing.T) {
		root := []byte("new-merkle-root-value")
		err := repo.UpdateMerkleRoot(ctx, eventID, root)
		require.NoError(t, err)

		got, err := repo.GetMerkleRoot(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, root, got)
	})

	t.Run("update replaces existing root", func(t *testing.T) {
		first := []byte("first-root")
		err := repo.UpdateMerkleRoot(ctx, eventID, first)
		require.NoError(t, err)

		second := []byte("second-root")
		err = repo.UpdateMerkleRoot(ctx, eventID, second)
		require.NoError(t, err)

		got, err := repo.GetMerkleRoot(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, second, got)
	})

	t.Run("non-existent event returns NotFound", func(t *testing.T) {
		err := repo.UpdateMerkleRoot(ctx, "018b2f19-e591-7d12-bf9e-000000000000", []byte("root"))
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		err := repo.UpdateMerkleRoot(ctx, "", []byte("root"))
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestEventEntryRepository_GetTicketLeafIndex(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewEventEntryRepository(testDB)
	ticketRepo := rdb.NewTicketRepository(testDB)
	ctx := context.Background()
	eventID, userID := seedTicketTestData(t)

	// Create a second user.
	var userID2 string
	err := testDB.Pool.QueryRow(ctx,
		`INSERT INTO users (name, email, external_id) VALUES ($1, $2, $3) RETURNING id`,
		"leaf-index-user2", "leaf-index2@example.com", "018b2f19-e591-7d12-bf9e-f0e74f1b4901",
	).Scan(&userID2)
	require.NoError(t, err)

	// Create a third user.
	var userID3 string
	err = testDB.Pool.QueryRow(ctx,
		`INSERT INTO users (name, email, external_id) VALUES ($1, $2, $3) RETURNING id`,
		"leaf-index-user3", "leaf-index3@example.com", "018b2f19-e591-7d12-bf9e-f0e74f1b4902",
	).Scan(&userID3)
	require.NoError(t, err)

	// Mint tickets in a specific order: user1 first, then user2, then user3.
	_, err = ticketRepo.Create(ctx, &entity.NewTicket{EventID: eventID, UserID: userID, TokenID: 1, TxHash: "0x1"})
	require.NoError(t, err)
	_, err = ticketRepo.Create(ctx, &entity.NewTicket{EventID: eventID, UserID: userID2, TokenID: 2, TxHash: "0x2"})
	require.NoError(t, err)
	_, err = ticketRepo.Create(ctx, &entity.NewTicket{EventID: eventID, UserID: userID3, TokenID: 3, TxHash: "0x3"})
	require.NoError(t, err)

	t.Run("returns correct 0-based index ordered by minted_at", func(t *testing.T) {
		idx, err := repo.GetTicketLeafIndex(ctx, eventID, userID)
		require.NoError(t, err)
		assert.Equal(t, 0, idx) // first minted

		idx, err = repo.GetTicketLeafIndex(ctx, eventID, userID2)
		require.NoError(t, err)
		assert.Equal(t, 1, idx) // second minted

		idx, err = repo.GetTicketLeafIndex(ctx, eventID, userID3)
		require.NoError(t, err)
		assert.Equal(t, 2, idx) // third minted
	})

	t.Run("user with no ticket returns NotFound", func(t *testing.T) {
		_, err := repo.GetTicketLeafIndex(ctx, eventID, "018b2f19-e591-7d12-bf9e-000000000000")
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		_, err := repo.GetTicketLeafIndex(ctx, "", userID)
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("empty user ID returns error", func(t *testing.T) {
		_, err := repo.GetTicketLeafIndex(ctx, eventID, "")
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
