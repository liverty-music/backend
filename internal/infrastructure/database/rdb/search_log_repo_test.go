package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchLogRepository_Upsert(t *testing.T) {
	cleanDatabase()
	searchLogRepo := rdb.NewSearchLogRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	// Setup: Create test artist (FK constraint)
	testArtist := &entity.Artist{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49d1",
		Name: "Search Log Test Artist",
	}
	_, err := artistRepo.Create(ctx, testArtist)
	require.NoError(t, err)

	t.Run("insert new search log", func(t *testing.T) {
		err := searchLogRepo.Upsert(ctx, testArtist.ID)
		require.NoError(t, err)

		log, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)
		assert.Equal(t, testArtist.ID, log.ArtistID)
		assert.WithinDuration(t, time.Now(), log.SearchTime, 5*time.Second)
	})

	t.Run("update existing search log", func(t *testing.T) {
		// Get the first log timestamp
		logBefore, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)

		// Wait briefly to ensure timestamp difference
		time.Sleep(10 * time.Millisecond)

		// Upsert again
		err = searchLogRepo.Upsert(ctx, testArtist.ID)
		require.NoError(t, err)

		logAfter, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)
		assert.True(t, logAfter.SearchTime.After(logBefore.SearchTime) || logAfter.SearchTime.Equal(logBefore.SearchTime))
	})
}

func TestSearchLogRepository_GetByArtistID(t *testing.T) {
	cleanDatabase()
	searchLogRepo := rdb.NewSearchLogRepository(testDB)
	ctx := context.Background()

	t.Run("not found", func(t *testing.T) {
		log, err := searchLogRepo.GetByArtistID(ctx, "018b2f19-e591-7d12-bf9e-f0e74f1b49d0")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Nil(t, log)
	})
}
