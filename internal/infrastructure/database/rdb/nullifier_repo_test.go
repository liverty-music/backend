package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNullifierRepository_Insert(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewNullifierRepository(testDB)
	ctx := context.Background()
	eventID := seedMerkleTestData(t) // reuse: creates venue + event

	t.Run("insert new nullifier", func(t *testing.T) {
		err := repo.Insert(ctx, eventID, testHash32("nullifier-hash-001"))
		require.NoError(t, err)
	})

	t.Run("duplicate nullifier returns AlreadyExists", func(t *testing.T) {
		hash := testHash32("nullifier-hash-dup")

		err := repo.Insert(ctx, eventID, hash)
		require.NoError(t, err)

		// Second insert with same event + nullifier should fail.
		err = repo.Insert(ctx, eventID, hash)
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	t.Run("same nullifier hash for different events succeeds", func(t *testing.T) {
		// Create a second event. Retrieve venue_id and artist_id from the existing event.
		var venueID, artistID string
		err := testDB.Pool.QueryRow(ctx,
			`SELECT venue_id, artist_id FROM events WHERE id = $1`,
			eventID,
		).Scan(&venueID, &artistID)
		require.NoError(t, err)

		eventID2 := seedEvent(t, venueID, artistID, "second event", "2026-04-01")

		hash := testHash32("shared-null-hash")

		err = repo.Insert(ctx, eventID, hash)
		require.NoError(t, err)

		// Same hash for different event should succeed.
		err = repo.Insert(ctx, eventID2, hash)
		require.NoError(t, err)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		err := repo.Insert(ctx, "", []byte("hash"))
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("empty nullifier hash returns error", func(t *testing.T) {
		err := repo.Insert(ctx, eventID, []byte{})
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestNullifierRepository_Exists(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewNullifierRepository(testDB)
	ctx := context.Background()
	eventID := seedMerkleTestData(t)

	t.Run("returns false for non-existent nullifier", func(t *testing.T) {
		exists, err := repo.Exists(ctx, eventID, testHash32("non-existent"))
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("returns true for existing nullifier", func(t *testing.T) {
		hash := testHash32("existing-null")

		err := repo.Insert(ctx, eventID, hash)
		require.NoError(t, err)

		exists, err := repo.Exists(ctx, eventID, hash)
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("returns false for same hash but different event", func(t *testing.T) {
		hash := testHash32("event-scoped")

		err := repo.Insert(ctx, eventID, hash)
		require.NoError(t, err)

		// Check against a different event ID that doesn't have this nullifier.
		exists, err := repo.Exists(ctx, "018b2f19-e591-7d12-bf9e-000000000000", hash)
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		_, err := repo.Exists(ctx, "", []byte("hash"))
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("empty nullifier hash returns error", func(t *testing.T) {
		_, err := repo.Exists(ctx, eventID, []byte{})
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
