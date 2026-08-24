package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

func TestSuppressedConcertRepository(t *testing.T) {
	repo := rdb.NewSuppressedConcertRepository(testDB)
	ctx := context.Background()
	localDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 19, 0, 0, 0, time.UTC)

	newEntry := func(venueID string, startTime *time.Time) *entity.SuppressedConcert {
		return &entity.SuppressedConcert{
			ID:             uuid.NewV7().String(),
			VenueID:        venueID,
			LocalEventDate: localDate,
			StartTime:      startTime,
		}
	}

	t.Run("insert then exists matches the key; delete un-suppresses", func(t *testing.T) {
		cleanDatabase(t)
		venueID := uuid.NewV7().String()

		require.NoError(t, repo.Insert(ctx, newEntry(venueID, &start)))

		got, err := repo.Exists(ctx, venueID, localDate, &start)
		require.NoError(t, err)
		assert.True(t, got)

		// A different start time at the same venue/date is a distinct slot.
		other := start.Add(3 * time.Hour)
		got, err = repo.Exists(ctx, venueID, localDate, &other)
		require.NoError(t, err)
		assert.False(t, got)

		// Un-suppress removes the entry.
		require.NoError(t, repo.Delete(ctx, venueID, localDate, &start))
		got, err = repo.Exists(ctx, venueID, localDate, &start)
		require.NoError(t, err)
		assert.False(t, got)
	})

	t.Run("nil start collapses the slot (NULLS NOT DISTINCT)", func(t *testing.T) {
		cleanDatabase(t)
		venueID := uuid.NewV7().String()
		require.NoError(t, repo.Insert(ctx, newEntry(venueID, nil)))

		got, err := repo.Exists(ctx, venueID, localDate, nil)
		require.NoError(t, err)
		assert.True(t, got, "a nil start must match a suppressed unknown-start slot")
	})

	t.Run("insert is idempotent on the natural key", func(t *testing.T) {
		cleanDatabase(t)
		venueID := uuid.NewV7().String()
		require.NoError(t, repo.Insert(ctx, newEntry(venueID, &start)))
		require.NoError(t, repo.Insert(ctx, newEntry(venueID, &start))) // ON CONFLICT DO NOTHING

		var count int
		require.NoError(t, testDB.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM suppressed_concerts WHERE venue_id = $1`, venueID).Scan(&count))
		assert.Equal(t, 1, count)
	})
}

func TestConcertRepository_DeleteAndSuppress(t *testing.T) {
	repo := rdb.NewConcertRepository(testDB)
	suppressedRepo := rdb.NewSuppressedConcertRepository(testDB)
	ctx := context.Background()
	localDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	t.Run("deletes the event and records a suppression from its natural key", func(t *testing.T) {
		cleanDatabase(t)
		venueID := seedVenue(t, "Venue One")
		artistID := seedArtist(t, "Artist One", uuid.NewV7().String())
		eventID := seedEvent(t, venueID, artistID, "Show", "2026-08-01")

		require.NoError(t, repo.DeleteAndSuppress(ctx, eventID))

		// The event is gone.
		var count int
		require.NoError(t, testDB.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM events WHERE id = $1`, eventID).Scan(&count))
		assert.Equal(t, 0, count)

		// Suppression is recorded for the event's natural key (start_at is NULL here).
		got, err := suppressedRepo.Exists(ctx, venueID, localDate, nil)
		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("absent event id records no suppression (idempotent)", func(t *testing.T) {
		cleanDatabase(t)
		require.NoError(t, repo.DeleteAndSuppress(ctx, uuid.NewV7().String()))

		var count int
		require.NoError(t, testDB.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM suppressed_concerts`).Scan(&count))
		assert.Equal(t, 0, count)
	})
}
