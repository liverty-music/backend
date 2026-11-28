package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

func TestRejectedConcertRepository_Append(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewRejectedConcertRepository(db)
	ctx := context.Background()

	t.Run("appends a rejection log entry", func(t *testing.T) {
		cleanTables(t, db)

		// rejected_concerts.artist_id has no FK, so no artist seed needed.
		artistID := uuid.NewV7().String()
		reviewedBy := "admin@example.com"
		localDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
		placeID := "place-test"
		adminArea := "JP-13"
		sourceURL := "https://example.com/show"
		reason := "wrong date"

		entry := &entity.RejectedConcert{
			ID:              uuid.NewV7().String(),
			ArtistID:        artistID,
			ArtistName:      "Test Artist",
			Title:           "Rejected Show",
			LocalDate:       localDate,
			ListedVenueName: "Test Venue",
			AdminArea:       &adminArea,
			SourceURL:       &sourceURL,
			ResolvedPlaceID: &placeID,
			TimeZone:        new("Asia/Tokyo"),
			Reason:          reason,
			ReviewedBy:      &reviewedBy,
		}

		err := repo.Append(ctx, entry)
		require.NoError(t, err)

		var zone string
		err = db.Pool.QueryRow(ctx,
			`SELECT time_zone FROM rejected_concerts WHERE id = $1`, entry.ID,
		).Scan(&zone)
		require.NoError(t, err)
		assert.Equal(t, "Asia/Tokyo", zone, "the candidate's time zone is kept")
	})

	t.Run("appends multiple entries for the same artist independently", func(t *testing.T) {
		cleanTables(t, db)

		artistID := uuid.NewV7().String()
		localDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

		for i := range 3 {
			entry := &entity.RejectedConcert{
				ID:              uuid.NewV7().String(),
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
		err := db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM rejected_concerts WHERE artist_id = $1`, artistID,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 3, count)
	})

	// @spec components/entity/rejected-concert/append "Two rejections of the same concert"
	t.Run("appending the same concert twice keeps two entries", func(t *testing.T) {
		cleanTables(t, db)

		artistID := uuid.NewV7().String()
		concert := func() *entity.RejectedConcert {
			return &entity.RejectedConcert{
				ID:              uuid.NewV7().String(),
				ArtistID:        artistID,
				ArtistName:      "Test Artist",
				Title:           "Show",
				LocalDate:       time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
				ListedVenueName: "Venue",
				Reason:          "test rejection",
			}
		}
		require.NoError(t, repo.Append(ctx, concert()))
		require.NoError(t, repo.Append(ctx, concert()))

		var count int
		err := db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM rejected_concerts WHERE artist_id = $1`, artistID,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("nil reviewed_by is stored as NULL", func(t *testing.T) {
		cleanTables(t, db)

		artistID := uuid.NewV7().String()
		entryID := uuid.NewV7().String()

		entry := &entity.RejectedConcert{
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
		err := db.Pool.QueryRow(ctx,
			`SELECT reviewed_by FROM rejected_concerts WHERE id = $1`, entryID,
		).Scan(&reviewedBy)
		require.NoError(t, err)
		assert.Nil(t, reviewedBy)
	})
}

func TestRejectedConcertRepository_ListByArtist(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewRejectedConcertRepository(db)
	ctx := context.Background()

	appendEntry := func(t *testing.T, artistID, venue string, start *entity.TimeOfDay) *entity.RejectedConcert {
		t.Helper()
		e := &entity.RejectedConcert{
			ID:              uuid.NewV7().String(),
			ArtistID:        artistID,
			ArtistName:      "Test Artist",
			Title:           "Show",
			LocalDate:       time.Date(2027, 1, 25, 0, 0, 0, 0, time.UTC),
			StartTime:       start,
			ListedVenueName: venue,
			Reason:          "duplicate",
		}
		require.NoError(t, repo.Append(ctx, e))
		return e
	}

	// @spec components/entity/rejected-concert/list-by-artist "Two entries"
	t.Run("returns the artist's entries with their listing, oldest first", func(t *testing.T) {
		cleanTables(t, db)
		artistID := uuid.NewV7().String()
		first := appendEntry(t, artistID, "【振替公演】仙台GIGS", &entity.TimeOfDay{Hour: 19})
		second := appendEntry(t, artistID, "Zepp Haneda", nil)
		appendEntry(t, uuid.NewV7().String(), "Zepp Namba", nil)

		got, err := repo.ListByArtist(ctx, artistID)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, first.ID, got[0].ID)
		assert.Equal(t, artistID, got[0].ArtistID)
		assert.Equal(t, "【振替公演】仙台GIGS", got[0].ListedVenueName)
		assert.True(t, first.LocalDate.Equal(got[0].LocalDate), "local date")
		assert.Equal(t, &entity.TimeOfDay{Hour: 19}, got[0].StartTime)
		assert.Equal(t, second.ID, got[1].ID)
		assert.Nil(t, got[1].StartTime, "an unknown start time stays unknown")
	})

	// @spec components/entity/rejected-concert/list-by-artist "None"
	t.Run("returns an empty list for an artist with no entry", func(t *testing.T) {
		cleanTables(t, db)
		appendEntry(t, uuid.NewV7().String(), "Zepp Namba", nil)

		got, err := repo.ListByArtist(ctx, uuid.NewV7().String())

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("fails with InvalidArgument without an artist", func(t *testing.T) {
		_, err := repo.ListByArtist(ctx, "")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
