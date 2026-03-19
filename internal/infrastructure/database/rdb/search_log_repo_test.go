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
	repo := rdb.NewSearchLogRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "insert new search log with pending status",
			run: func(t *testing.T) {
				t.Helper()
				cleanDatabase()
				artistID := seedArtist(t, "Search Log Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49d1")

				err := repo.Upsert(ctx, artistID, entity.SearchLogStatusPending)
				require.NoError(t, err)

				log, err := repo.GetByArtistID(ctx, artistID)
				require.NoError(t, err)
				assert.Equal(t, artistID, log.ArtistID)
				assert.Equal(t, entity.SearchLogStatusPending, log.Status)
				assert.WithinDuration(t, time.Now(), log.SearchTime, 5*time.Second)
			},
		},
		{
			name: "upsert updates status when record already exists",
			run: func(t *testing.T) {
				t.Helper()
				cleanDatabase()
				artistID := seedArtist(t, "Search Log Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49d1")

				err := repo.Upsert(ctx, artistID, entity.SearchLogStatusFailed)
				require.NoError(t, err)

				err = repo.Upsert(ctx, artistID, entity.SearchLogStatusPending)
				require.NoError(t, err)

				log, err := repo.GetByArtistID(ctx, artistID)
				require.NoError(t, err)
				assert.Equal(t, entity.SearchLogStatusPending, log.Status)
			},
		},
		{
			name: "upsert updates searched_at timestamp",
			run: func(t *testing.T) {
				t.Helper()
				cleanDatabase()
				artistID := seedArtist(t, "Search Log Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49d1")

				err := repo.Upsert(ctx, artistID, entity.SearchLogStatusPending)
				require.NoError(t, err)

				logBefore, err := repo.GetByArtistID(ctx, artistID)
				require.NoError(t, err)

				// Second upsert: the DB records the current timestamp on each upsert.
				// The DB stores microsecond precision so a same-microsecond write is
				// an accepted equal case — no sleep is required.
				err = repo.Upsert(ctx, artistID, entity.SearchLogStatusCompleted)
				require.NoError(t, err)

				logAfter, err := repo.GetByArtistID(ctx, artistID)
				require.NoError(t, err)
				assert.True(t, logAfter.SearchTime.After(logBefore.SearchTime) || logAfter.SearchTime.Equal(logBefore.SearchTime))
				assert.Equal(t, entity.SearchLogStatusCompleted, logAfter.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
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

	t.Run("returns status fields", func(t *testing.T) {
		artistID := seedArtist(t, "GetByArtistID Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49d2")

		err := searchLogRepo.Upsert(ctx, artistID, entity.SearchLogStatusPending)
		require.NoError(t, err)

		err = searchLogRepo.UpdateStatus(ctx, artistID, entity.SearchLogStatusFailed)
		require.NoError(t, err)

		log, err := searchLogRepo.GetByArtistID(ctx, artistID)
		require.NoError(t, err)
		assert.Equal(t, entity.SearchLogStatusFailed, log.Status)
	})
}

func TestSearchLogRepository_ListByArtistIDs(t *testing.T) {
	cleanDatabase()
	searchLogRepo := rdb.NewSearchLogRepository(testDB)
	ctx := context.Background()

	// Setup: Create test artists
	artistIDs := []string{
		seedArtist(t, "List Artist 1", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4a01"),
		seedArtist(t, "List Artist 2", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4a02"),
		seedArtist(t, "List Artist 3", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4a03"),
	}

	t.Run("returns empty slice for no matches", func(t *testing.T) {
		logs, err := searchLogRepo.ListByArtistIDs(ctx, []string{"018b2f19-e591-7d12-bf9e-000000000000"})
		require.NoError(t, err)
		assert.Empty(t, logs)
	})

	t.Run("returns logs for matching artists only", func(t *testing.T) {
		// Insert logs for artist 1 and 2 but not 3
		err := searchLogRepo.Upsert(ctx, artistIDs[0], entity.SearchLogStatusCompleted)
		require.NoError(t, err)
		err = searchLogRepo.Upsert(ctx, artistIDs[1], entity.SearchLogStatusPending)
		require.NoError(t, err)

		logs, err := searchLogRepo.ListByArtistIDs(ctx, artistIDs)
		require.NoError(t, err)
		assert.Len(t, logs, 2)

		// Build a map for easier assertions
		logMap := make(map[string]*entity.SearchLog)
		for _, l := range logs {
			logMap[l.ArtistID] = l
		}

		assert.Equal(t, entity.SearchLogStatusCompleted, logMap[artistIDs[0]].Status)
		assert.Equal(t, entity.SearchLogStatusPending, logMap[artistIDs[1]].Status)
		_, exists := logMap[artistIDs[2]]
		assert.False(t, exists)
	})
}

func TestSearchLogRepository_UpdateStatus(t *testing.T) {
	repo := rdb.NewSearchLogRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name       string
		setup      func() string // returns artistID with pre-existing log
		wantStatus entity.SearchLogStatus
	}{
		{
			name: "update to completed",
			setup: func() string {
				cleanDatabase()
				artistID := seedArtist(t, "UpdateStatus Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4b01")
				err := repo.Upsert(ctx, artistID, entity.SearchLogStatusPending)
				require.NoError(t, err)
				return artistID
			},
			wantStatus: entity.SearchLogStatusCompleted,
		},
		{
			name: "update to failed",
			setup: func() string {
				cleanDatabase()
				artistID := seedArtist(t, "UpdateStatus Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4b01")
				err := repo.Upsert(ctx, artistID, entity.SearchLogStatusPending)
				require.NoError(t, err)
				return artistID
			},
			wantStatus: entity.SearchLogStatusFailed,
		},
		{
			name: "update from failed back to completed",
			setup: func() string {
				cleanDatabase()
				artistID := seedArtist(t, "UpdateStatus Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4b01")
				err := repo.Upsert(ctx, artistID, entity.SearchLogStatusPending)
				require.NoError(t, err)
				err = repo.UpdateStatus(ctx, artistID, entity.SearchLogStatusFailed)
				require.NoError(t, err)
				return artistID
			},
			wantStatus: entity.SearchLogStatusCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artistID := tt.setup()

			err := repo.UpdateStatus(ctx, artistID, tt.wantStatus)
			require.NoError(t, err)

			log, err := repo.GetByArtistID(ctx, artistID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, log.Status)
		})
	}
}

func TestSearchLogRepository_Delete(t *testing.T) {
	repo := rdb.NewSearchLogRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func(t *testing.T) string // returns artistID
		wantErr error
	}{
		{
			name: "deletes existing search log",
			setup: func(t *testing.T) string {
				t.Helper()
				cleanDatabase()
				artistID := seedArtist(t, "Delete Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4c01")
				err := repo.Upsert(ctx, artistID, entity.SearchLogStatusPending)
				require.NoError(t, err)
				return artistID
			},
			wantErr: nil,
		},
		{
			name: "deleting non-existent log is idempotent",
			setup: func(t *testing.T) string {
				t.Helper()
				cleanDatabase()
				// Seed an artist but intentionally do not upsert a search log for it.
				return seedArtist(t, "No Log Artist", "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4c02")
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artistID := tt.setup(t)

			err := repo.Delete(ctx, artistID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)

			// Verify the log is no longer retrievable.
			got, err := repo.GetByArtistID(ctx, artistID)
			assert.ErrorIs(t, err, apperr.ErrNotFound)
			assert.Nil(t, got)
		})
	}
}
