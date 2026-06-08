package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRejectedConcertLogRepository_Append(t *testing.T) {
	repo := rdb.NewRejectedConcertLogRepository(testDB)
	ctx := context.Background()

	t.Run("appends a rejection log entry", func(t *testing.T) {
		cleanDatabase(t)

		// rejected_concerts_log.artist_id has no FK, so no artist seed needed.
		artistID := uuid.Must(uuid.NewV7()).String()
		reviewedBy := "admin@example.com"
		localDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
		placeID := "place-test"
		venueName := "Test Venue Canonical"
		adminArea := "JP-13"
		sourceURL := "https://example.com/show"
		reason := "wrong date"

		entry := &entity.RejectedConcertLog{
			ID:                uuid.Must(uuid.NewV7()).String(),
			ArtistID:          artistID,
			ArtistName:        "Test Artist",
			Title:             "Rejected Show",
			LocalDate:         localDate,
			ListedVenueName:   "Test Venue",
			AdminArea:         &adminArea,
			SourceURL:         &sourceURL,
			ResolvedPlaceID:   &placeID,
			ResolvedVenueName: &venueName,
			ResolvedAdminArea: &adminArea,
			Reason:            reason,
			ReviewedBy:        &reviewedBy,
		}

		err := repo.Append(ctx, entry)
		require.NoError(t, err)

		// Verify the row was inserted by counting rows in the table.
		var count int
		err = testDB.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM rejected_concerts_log WHERE id = $1`, entry.ID,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("appends multiple entries for the same artist independently", func(t *testing.T) {
		cleanDatabase(t)

		artistID := uuid.Must(uuid.NewV7()).String()
		localDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

		for i := range 3 {
			entry := &entity.RejectedConcertLog{
				ID:              uuid.Must(uuid.NewV7()).String(),
				ArtistID:        artistID,
				ArtistName:      "Test Artist",
				Title:           "Show",
				LocalDate:       localDate.AddDate(0, 0, i),
				ListedVenueName: "Venue",
				Reason:          "test rejection",
			}
			require.NoError(t, repo.Append(ctx, entry))
		}

		var count int
		err := testDB.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM rejected_concerts_log WHERE artist_id = $1`, artistID,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 3, count)
	})

	t.Run("nil reviewed_by is stored as NULL", func(t *testing.T) {
		cleanDatabase(t)

		artistID := uuid.Must(uuid.NewV7()).String()
		entryID := uuid.Must(uuid.NewV7()).String()

		entry := &entity.RejectedConcertLog{
			ID:              entryID,
			ArtistID:        artistID,
			ArtistName:      "Anonymous",
			Title:           "Show",
			LocalDate:       time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			ListedVenueName: "Venue",
			Reason:          "no reason given",
			ReviewedBy:      nil,
		}

		require.NoError(t, repo.Append(ctx, entry))

		var reviewedBy *string
		err := testDB.Pool.QueryRow(ctx,
			`SELECT reviewed_by FROM rejected_concerts_log WHERE id = $1`, entryID,
		).Scan(&reviewedBy)
		require.NoError(t, err)
		assert.Nil(t, reviewedBy)
	})
}
