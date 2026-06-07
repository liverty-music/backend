package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildStagedConcert returns a minimal StagedConcert for the given artist.
func buildStagedConcert(t *testing.T, artistID string) *entity.StagedConcert {
	t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	localDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	listedVenue := "Zepp Tokyo"
	sourceURL := "https://example.com/show"
	return &entity.StagedConcert{
		ID:              id,
		ArtistID:        artistID,
		Title:           "Test Concert",
		LocalDate:       localDate,
		ListedVenueName: listedVenue,
		SourceURL:       &sourceURL,
	}
}

// buildStagedConcertWithPlace returns a StagedConcert with a resolved place ID.
func buildStagedConcertWithPlace(t *testing.T, artistID, placeID, venueName string) *entity.StagedConcert {
	t.Helper()
	sc := buildStagedConcert(t, artistID)
	sc.ResolvedPlaceID = &placeID
	sc.ResolvedVenueName = &venueName
	return sc
}

func TestStagedConcertRepository_Upsert_Insert(t *testing.T) {
	repo := rdb.NewStagedConcertRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	artistID := seedArtist(t, "Stage Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000001")

	sc := buildStagedConcert(t, artistID)

	err := repo.Upsert(ctx, sc)
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	assert.Equal(t, sc.ID, got.ID)
	assert.Equal(t, sc.ArtistID, got.ArtistID)
	assert.Equal(t, sc.Title, got.Title)
	assert.Equal(t, sc.ListedVenueName, got.ListedVenueName)
	assert.WithinDuration(t, time.Now(), got.DiscoveredTime, 5*time.Second)
}

func TestStagedConcertRepository_Upsert_RefreshOnConflict_ByListedName(t *testing.T) {
	repo := rdb.NewStagedConcertRepository(testDB)
	ctx := context.Background()

	// Natural key: (artist_id, local_date, listed_venue_name) when resolved_place_id IS NULL.
	t.Run("refresh by listed name keeps original discovered_at and no duplicate", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Listed Name Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000002")

		first := buildStagedConcert(t, artistID)
		err := repo.Upsert(ctx, first)
		require.NoError(t, err)

		firstRow, err := repo.GetByID(ctx, first.ID)
		require.NoError(t, err)
		originalDiscovered := firstRow.DiscoveredTime

		// Upsert again with same (artist, date, listed_venue_name) but different ID + new title.
		second := buildStagedConcert(t, artistID)
		second.Title = "Updated Title"

		err = repo.Upsert(ctx, second)
		require.NoError(t, err)

		// Only one row should exist (no duplicate).
		pending, err := repo.ListPending(ctx)
		require.NoError(t, err)
		assert.Len(t, pending, 1)

		// The title is updated, but discovered_at is preserved from the first insert.
		assert.Equal(t, "Updated Title", pending[0].Title)
		assert.WithinDuration(t, originalDiscovered, pending[0].DiscoveredTime, time.Second)
	})
}

func TestStagedConcertRepository_Upsert_RefreshOnConflict_ByPlaceID(t *testing.T) {
	repo := rdb.NewStagedConcertRepository(testDB)
	ctx := context.Background()

	t.Run("refresh by place id keeps original discovered_at and no duplicate", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Place ID Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000003")

		first := buildStagedConcertWithPlace(t, artistID, "place-abc", "Canonical Venue")
		err := repo.Upsert(ctx, first)
		require.NoError(t, err)

		firstRow, err := repo.GetByID(ctx, first.ID)
		require.NoError(t, err)
		originalDiscovered := firstRow.DiscoveredTime

		// Second upsert: same (artist, date, place_id) but new title.
		second := buildStagedConcertWithPlace(t, artistID, "place-abc", "Canonical Venue Updated")
		second.Title = "Refreshed Title"

		err = repo.Upsert(ctx, second)
		require.NoError(t, err)

		pending, err := repo.ListPending(ctx)
		require.NoError(t, err)
		assert.Len(t, pending, 1)
		assert.Equal(t, "Refreshed Title", pending[0].Title)
		assert.WithinDuration(t, originalDiscovered, pending[0].DiscoveredTime, time.Second)
	})
}

func TestStagedConcertRepository_Upsert_BothNaturalKeyPaths(t *testing.T) {
	repo := rdb.NewStagedConcertRepository(testDB)
	ctx := context.Background()

	t.Run("resolved place id path", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Place Path Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000004")

		sc := buildStagedConcertWithPlace(t, artistID, "place-xyz", "Venue XYZ")
		err := repo.Upsert(ctx, sc)
		require.NoError(t, err)

		got, err := repo.GetByID(ctx, sc.ID)
		require.NoError(t, err)
		require.NotNil(t, got.ResolvedPlaceID)
		assert.Equal(t, "place-xyz", *got.ResolvedPlaceID)
		require.NotNil(t, got.ResolvedVenueName)
		assert.Equal(t, "Venue XYZ", *got.ResolvedVenueName)
	})

	t.Run("null place id fallback path", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Fallback Path Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000005")

		sc := buildStagedConcert(t, artistID) // no ResolvedPlaceID
		err := repo.Upsert(ctx, sc)
		require.NoError(t, err)

		got, err := repo.GetByID(ctx, sc.ID)
		require.NoError(t, err)
		assert.Nil(t, got.ResolvedPlaceID)
		assert.Equal(t, sc.ListedVenueName, got.ListedVenueName)
	})
}

func TestStagedConcertRepository_Delete(t *testing.T) {
	repo := rdb.NewStagedConcertRepository(testDB)
	ctx := context.Background()

	t.Run("deletes existing row", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Delete Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000006")

		sc := buildStagedConcert(t, artistID)
		require.NoError(t, repo.Upsert(ctx, sc))

		err := repo.Delete(ctx, sc.ID)
		require.NoError(t, err)

		_, err = repo.GetByID(ctx, sc.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("idempotent when row does not exist", func(t *testing.T) {
		cleanDatabase(t)

		err := repo.Delete(ctx, uuid.Must(uuid.NewV7()).String())
		assert.NoError(t, err)
	})
}

func TestStagedConcertRepository_ListPending_Order(t *testing.T) {
	repo := rdb.NewStagedConcertRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	artistID := seedArtist(t, "Order Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000007")

	// Insert three rows with different titles; discovered_at is set by the DB to NOW()
	// so they will be in insertion order.
	sc1 := buildStagedConcert(t, artistID)
	sc1.Title = "First"
	require.NoError(t, repo.Upsert(ctx, sc1))

	sc2 := buildStagedConcert(t, artistID)
	sc2.Title = "Second"
	sc2.LocalDate = sc2.LocalDate.AddDate(0, 0, 1) // different date to avoid natural-key conflict
	require.NoError(t, repo.Upsert(ctx, sc2))

	sc3 := buildStagedConcert(t, artistID)
	sc3.Title = "Third"
	sc3.LocalDate = sc3.LocalDate.AddDate(0, 0, 2)
	require.NoError(t, repo.Upsert(ctx, sc3))

	got, err := repo.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, got, 3)

	// discovered_at ASC means insertion order.
	assert.True(t, !got[0].DiscoveredTime.After(got[1].DiscoveredTime))
	assert.True(t, !got[1].DiscoveredTime.After(got[2].DiscoveredTime))
}

func TestStagedConcertRepository_GetByID_NotFound(t *testing.T) {
	repo := rdb.NewStagedConcertRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)

	_, err := repo.GetByID(ctx, uuid.Must(uuid.NewV7()).String())
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

func TestStagedConcertRepository_ListPendingDedupKeysByArtist(t *testing.T) {
	repo := rdb.NewStagedConcertRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	artistA := seedArtist(t, "Artist A", "aaaaaaaa-aaaa-aaaa-aaaa-100000000008")
	artistB := seedArtist(t, "Artist B", "aaaaaaaa-aaaa-aaaa-aaaa-100000000009")

	date1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	// Artist A — two staged concerts.
	scA1 := &entity.StagedConcert{
		ID: uuid.Must(uuid.NewV7()).String(), ArtistID: artistA, Title: "Show 1",
		LocalDate: date1, ListedVenueName: "Venue Alpha",
	}
	scA2 := &entity.StagedConcert{
		ID: uuid.Must(uuid.NewV7()).String(), ArtistID: artistA, Title: "Show 2",
		LocalDate: date2, ListedVenueName: "Venue Beta",
	}
	// Artist B — one staged concert.
	scB1 := &entity.StagedConcert{
		ID: uuid.Must(uuid.NewV7()).String(), ArtistID: artistB, Title: "Show B",
		LocalDate: date1, ListedVenueName: "Venue Gamma",
	}
	require.NoError(t, repo.Upsert(ctx, scA1))
	require.NoError(t, repo.Upsert(ctx, scA2))
	require.NoError(t, repo.Upsert(ctx, scB1))

	keysA, err := repo.ListPendingDedupKeysByArtist(ctx, artistA)
	require.NoError(t, err)
	assert.Len(t, keysA, 2)

	venueNames := map[string]bool{}
	for _, k := range keysA {
		venueNames[k.ListedVenueName] = true
	}
	assert.True(t, venueNames["Venue Alpha"])
	assert.True(t, venueNames["Venue Beta"])
	assert.False(t, venueNames["Venue Gamma"], "Artist B's concerts must not appear in Artist A's keys")

	keysB, err := repo.ListPendingDedupKeysByArtist(ctx, artistB)
	require.NoError(t, err)
	assert.Len(t, keysB, 1)
	assert.Equal(t, "Venue Gamma", keysB[0].ListedVenueName)
}
