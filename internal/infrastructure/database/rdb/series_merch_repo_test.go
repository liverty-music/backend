package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedMerchSeries inserts a TOUR series (optionally with a merch_url) plus one
// event per dayOffset (relative to CURRENT_DATE), each linked to artistID via
// event_performers. Returns the series ID.
func seedMerchSeries(t *testing.T, venueID, artistID, title, merchURL string, dayOffsets ...int) string {
	t.Helper()
	ctx := context.Background()
	seriesID := uuid.Must(uuid.NewV7()).String()

	if merchURL == "" {
		_, err := testDB.Pool.Exec(ctx,
			`INSERT INTO series (id, title, type) VALUES ($1, $2, 'TOUR')`,
			seriesID, title,
		)
		require.NoError(t, err)
	} else {
		_, err := testDB.Pool.Exec(ctx,
			`INSERT INTO series (id, title, type, merch_url) VALUES ($1, $2, 'TOUR', $3)`,
			seriesID, title, merchURL,
		)
		require.NoError(t, err)
	}

	for _, off := range dayOffsets {
		eventID := uuid.Must(uuid.NewV7()).String()
		_, err := testDB.Pool.Exec(ctx,
			`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, CURRENT_DATE + $4::int)`,
			eventID, seriesID, venueID, off,
		)
		require.NoError(t, err)
		_, err = testDB.Pool.Exec(ctx,
			`INSERT INTO event_performers (event_id, artist_id) VALUES ($1, $2)`,
			eventID, artistID,
		)
		require.NoError(t, err)
	}
	return seriesID
}

func TestSeriesRepository_ListSeriesInMerchWindow(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewSeriesRepository(testDB)
	ctx := context.Background()

	venueID := seedVenue(t, "Budokan")
	artistID := seedArtist(t, "Window Artist", "11111111-1111-1111-1111-111111111111")

	inWindowEmpty := seedMerchSeries(t, venueID, artistID, "In Window Empty", "", 10)
	inWindowLive := seedMerchSeries(t, venueID, artistID, "In Window Live", "https://artist.example.com/goods", 20)
	// Earliest of two events (+50) is in window even though the other (+200) is not.
	multiEvent := seedMerchSeries(t, venueID, artistID, "Multi Event", "", 200, 50)
	// Excluded: earliest event beyond the 60-day window.
	_ = seedMerchSeries(t, venueID, artistID, "Beyond Window", "", 90)
	// Excluded: earliest event already in the past.
	_ = seedMerchSeries(t, venueID, artistID, "Past Event", "", -5)

	got, err := repo.ListSeriesInMerchWindow(ctx, 60*24*time.Hour)
	require.NoError(t, err)

	byID := make(map[string]string, len(got)) // seriesID -> merchURL
	titles := make(map[string]string, len(got))
	for _, c := range got {
		byID[c.SeriesID] = c.MerchURL
		titles[c.SeriesID] = c.SeriesTitle
		assert.Equal(t, "Window Artist", c.ArtistName, "representative artist name should be populated")
	}

	assert.Len(t, got, 3, "only the three in-window series should be returned")
	assert.Contains(t, byID, inWindowEmpty)
	assert.Contains(t, byID, inWindowLive)
	assert.Contains(t, byID, multiEvent)
	assert.Empty(t, byID[inWindowEmpty], "empty merch_url returned as empty string")
	assert.Equal(t, "https://artist.example.com/goods", byID[inWindowLive], "non-empty merch_url returned for revalidation")
	assert.Equal(t, "In Window Empty", titles[inWindowEmpty])
}

func TestSeriesRepository_SetMerchURL_FillOnce(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewSeriesRepository(testDB)
	ctx := context.Background()

	venueID := seedVenue(t, "Zepp")
	artistID := seedArtist(t, "FillOnce Artist", "22222222-2222-2222-2222-222222222222")
	seriesID := seedMerchSeries(t, venueID, artistID, "FillOnce", "", 5)

	// First set populates the empty field.
	require.NoError(t, repo.SetMerchURL(ctx, seriesID, "https://artist.example.com/first"))
	s, err := repo.Get(ctx, seriesID)
	require.NoError(t, err)
	assert.Equal(t, "https://artist.example.com/first", s.MerchURL)

	// Second set must NOT overwrite a populated (live) URL.
	require.NoError(t, repo.SetMerchURL(ctx, seriesID, "https://artist.example.com/second"))
	s, err = repo.Get(ctx, seriesID)
	require.NoError(t, err)
	assert.Equal(t, "https://artist.example.com/first", s.MerchURL, "fill-once must not overwrite a live URL")

	// Clearing resets to empty, after which a set succeeds again.
	require.NoError(t, repo.ClearMerchURL(ctx, seriesID))
	s, err = repo.Get(ctx, seriesID)
	require.NoError(t, err)
	assert.Empty(t, s.MerchURL)

	require.NoError(t, repo.SetMerchURL(ctx, seriesID, "https://artist.example.com/third"))
	s, err = repo.Get(ctx, seriesID)
	require.NoError(t, err)
	assert.Equal(t, "https://artist.example.com/third", s.MerchURL, "set succeeds after clear")
}
