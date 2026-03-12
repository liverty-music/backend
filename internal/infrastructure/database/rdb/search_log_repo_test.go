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
		MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49d1",
	}
	_, err := artistRepo.Create(ctx, testArtist)
	require.NoError(t, err)

	t.Run("insert new search log with pending status", func(t *testing.T) {
		err := searchLogRepo.Upsert(ctx, testArtist.ID, entity.SearchLogStatusPending)
		require.NoError(t, err)

		log, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)
		assert.Equal(t, testArtist.ID, log.ArtistID)
		assert.Equal(t, entity.SearchLogStatusPending, log.Status)
		assert.WithinDuration(t, time.Now(), log.SearchTime, 5*time.Second)
	})

	t.Run("upsert updates status", func(t *testing.T) {
		// First set to failed via UpdateStatus
		err := searchLogRepo.UpdateStatus(ctx, testArtist.ID, entity.SearchLogStatusFailed)
		require.NoError(t, err)

		// Upsert should update status
		err = searchLogRepo.Upsert(ctx, testArtist.ID, entity.SearchLogStatusPending)
		require.NoError(t, err)

		log, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.SearchLogStatusPending, log.Status)
	})

	t.Run("upsert updates searched_at timestamp", func(t *testing.T) {
		logBefore, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond)

		err = searchLogRepo.Upsert(ctx, testArtist.ID, entity.SearchLogStatusCompleted)
		require.NoError(t, err)

		logAfter, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)
		assert.True(t, logAfter.SearchTime.After(logBefore.SearchTime) || logAfter.SearchTime.Equal(logBefore.SearchTime))
		assert.Equal(t, entity.SearchLogStatusCompleted, logAfter.Status)
	})
}

func TestSearchLogRepository_GetByArtistID(t *testing.T) {
	cleanDatabase()
	searchLogRepo := rdb.NewSearchLogRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	t.Run("not found", func(t *testing.T) {
		log, err := searchLogRepo.GetByArtistID(ctx, "018b2f19-e591-7d12-bf9e-f0e74f1b49d0")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Nil(t, log)
	})

	t.Run("returns status fields", func(t *testing.T) {
		testArtist := &entity.Artist{
			ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49d2",
			Name: "GetByArtistID Test Artist",
			MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49d2",
		}
		_, err := artistRepo.Create(ctx, testArtist)
		require.NoError(t, err)

		err = searchLogRepo.Upsert(ctx, testArtist.ID, entity.SearchLogStatusPending)
		require.NoError(t, err)

		err = searchLogRepo.UpdateStatus(ctx, testArtist.ID, entity.SearchLogStatusFailed)
		require.NoError(t, err)

		log, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.SearchLogStatusFailed, log.Status)
	})
}

func TestSearchLogRepository_ListByArtistIDs(t *testing.T) {
	cleanDatabase()
	searchLogRepo := rdb.NewSearchLogRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	// Setup: Create test artists
	artists := []*entity.Artist{
		{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4a01", Name: "List Artist 1", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4a01"},
		{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4a02", Name: "List Artist 2", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4a02"},
		{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4a03", Name: "List Artist 3", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4a03"},
	}
	for _, a := range artists {
		_, err := artistRepo.Create(ctx, a)
		require.NoError(t, err)
	}

	t.Run("returns empty slice for no matches", func(t *testing.T) {
		logs, err := searchLogRepo.ListByArtistIDs(ctx, []string{"018b2f19-e591-7d12-bf9e-000000000000"})
		require.NoError(t, err)
		assert.Empty(t, logs)
	})

	t.Run("returns logs for matching artists only", func(t *testing.T) {
		// Insert logs for artist 1 and 2 but not 3
		err := searchLogRepo.Upsert(ctx, artists[0].ID, entity.SearchLogStatusCompleted)
		require.NoError(t, err)
		err = searchLogRepo.Upsert(ctx, artists[1].ID, entity.SearchLogStatusPending)
		require.NoError(t, err)

		logs, err := searchLogRepo.ListByArtistIDs(ctx, []string{artists[0].ID, artists[1].ID, artists[2].ID})
		require.NoError(t, err)
		assert.Len(t, logs, 2)

		// Build a map for easier assertions
		logMap := make(map[string]*entity.SearchLog)
		for _, l := range logs {
			logMap[l.ArtistID] = l
		}

		assert.Equal(t, entity.SearchLogStatusCompleted, logMap[artists[0].ID].Status)
		assert.Equal(t, entity.SearchLogStatusPending, logMap[artists[1].ID].Status)
		_, exists := logMap[artists[2].ID]
		assert.False(t, exists)
	})
}

func TestSearchLogRepository_UpdateStatus(t *testing.T) {
	cleanDatabase()
	searchLogRepo := rdb.NewSearchLogRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	testArtist := &entity.Artist{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b4b01",
		Name: "UpdateStatus Test Artist",
		MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4b01",
	}
	_, err := artistRepo.Create(ctx, testArtist)
	require.NoError(t, err)

	// Setup: Create initial search log as pending
	err = searchLogRepo.Upsert(ctx, testArtist.ID, entity.SearchLogStatusPending)
	require.NoError(t, err)

	t.Run("update to completed", func(t *testing.T) {
		err := searchLogRepo.UpdateStatus(ctx, testArtist.ID, entity.SearchLogStatusCompleted)
		require.NoError(t, err)

		log, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.SearchLogStatusCompleted, log.Status)
	})

	t.Run("update to failed", func(t *testing.T) {
		err := searchLogRepo.UpdateStatus(ctx, testArtist.ID, entity.SearchLogStatusFailed)
		require.NoError(t, err)

		log, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.SearchLogStatusFailed, log.Status)
	})

	t.Run("update back to completed", func(t *testing.T) {
		err := searchLogRepo.UpdateStatus(ctx, testArtist.ID, entity.SearchLogStatusCompleted)
		require.NoError(t, err)

		log, err := searchLogRepo.GetByArtistID(ctx, testArtist.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.SearchLogStatusCompleted, log.Status)
	})
}
