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
	"uuid"
)

// buildConcertCandidate returns a minimal ConcertCandidate for the given artist. It
// seeds a parent series row (concert_candidates.series_id is a real FK) and points
// the concert candidate at it.
func buildConcertCandidate(t *testing.T, db *rdb.Database, artistID string) *entity.ConcertCandidate {
	t.Helper()
	id := uuid.NewV7().String()
	localDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	listedVenue := "Zepp Tokyo"
	sourceURL := "https://example.com/show"
	return &entity.ConcertCandidate{
		ID:              id,
		ArtistID:        artistID,
		SeriesID:        seedSeriesOnly(t, db, "Test Series"),
		Title:           "Test Concert",
		LocalDate:       localDate,
		ListedVenueName: listedVenue,
		SourceURL:       &sourceURL,
	}
}

// buildConcertCandidateWithPlace returns a ConcertCandidate with a resolved place ID.
func buildConcertCandidateWithPlace(t *testing.T, db *rdb.Database, artistID, placeID, venueName string) *entity.ConcertCandidate {
	t.Helper()
	sc := buildConcertCandidate(t, db, artistID)
	sc.ResolvedPlaceID = &placeID
	sc.ResolvedVenueName = &venueName
	return sc
}

func TestConcertCandidateRepository_Upsert_Insert(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewConcertCandidateRepository(db)
	ctx := context.Background()

	artistID := seedArtist(t, db, "Stage Test Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000001")

	sc := buildConcertCandidate(t, db, artistID)

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

func TestConcertCandidateRepository_Upsert_RefreshOnConflict_ByListedName(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewConcertCandidateRepository(db)
	ctx := context.Background()

	// Natural key: (artist_id, local_date, listed_venue_name) when resolved_place_id IS NULL.
	t.Run("refresh by listed name keeps original discovered_at and no duplicate", func(t *testing.T) {
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Listed Name Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000002")

		first := buildConcertCandidate(t, db, artistID)
		err := repo.Upsert(ctx, first)
		require.NoError(t, err)

		firstRow, err := repo.GetByID(ctx, first.ID)
		require.NoError(t, err)
		originalDiscovered := firstRow.DiscoveredTime

		// Upsert again with same (artist, date, listed_venue_name) but different ID + new title.
		second := buildConcertCandidate(t, db, artistID)
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

func TestConcertCandidateRepository_Upsert_RefreshOnConflict_ByPlaceID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewConcertCandidateRepository(db)
	ctx := context.Background()

	t.Run("refresh by place id keeps original discovered_at and no duplicate", func(t *testing.T) {
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Place ID Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000003")

		first := buildConcertCandidateWithPlace(t, db, artistID, "place-abc", "Canonical Venue")
		err := repo.Upsert(ctx, first)
		require.NoError(t, err)

		firstRow, err := repo.GetByID(ctx, first.ID)
		require.NoError(t, err)
		originalDiscovered := firstRow.DiscoveredTime

		// Second upsert: same (artist, date, place_id) but new title.
		second := buildConcertCandidateWithPlace(t, db, artistID, "place-abc", "Canonical Venue Updated")
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

func TestConcertCandidateRepository_Upsert_BothNaturalKeyPaths(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewConcertCandidateRepository(db)
	ctx := context.Background()

	t.Run("resolved place id path", func(t *testing.T) {
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Place Path Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000004")

		sc := buildConcertCandidateWithPlace(t, db, artistID, "place-xyz", "Venue XYZ")
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
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Fallback Path Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000005")

		sc := buildConcertCandidate(t, db, artistID) // no ResolvedPlaceID
		err := repo.Upsert(ctx, sc)
		require.NoError(t, err)

		got, err := repo.GetByID(ctx, sc.ID)
		require.NoError(t, err)
		assert.Nil(t, got.ResolvedPlaceID)
		assert.Equal(t, sc.ListedVenueName, got.ListedVenueName)
	})
}

func TestConcertCandidateRepository_Delete(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewConcertCandidateRepository(db)
	ctx := context.Background()

	t.Run("deletes existing row", func(t *testing.T) {
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Delete Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000006")

		sc := buildConcertCandidate(t, db, artistID)
		require.NoError(t, repo.Upsert(ctx, sc))

		err := repo.Delete(ctx, sc.ID)
		require.NoError(t, err)

		_, err = repo.GetByID(ctx, sc.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("idempotent when row does not exist", func(t *testing.T) {
		cleanTables(t, db)

		err := repo.Delete(ctx, uuid.NewV7().String())
		assert.NoError(t, err)
	})
}

func TestConcertCandidateRepository_ListPending_Order(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewConcertCandidateRepository(db)
	ctx := context.Background()

	artistID := seedArtist(t, db, "Order Artist", "aaaaaaaa-aaaa-aaaa-aaaa-100000000007")

	// Insert three rows with different titles; discovered_at is set by the DB to NOW()
	// so they will be in insertion order.
	sc1 := buildConcertCandidate(t, db, artistID)
	sc1.Title = "First"
	require.NoError(t, repo.Upsert(ctx, sc1))

	sc2 := buildConcertCandidate(t, db, artistID)
	sc2.Title = "Second"
	sc2.LocalDate = sc2.LocalDate.AddDate(0, 0, 1) // different date to avoid natural-key conflict
	require.NoError(t, repo.Upsert(ctx, sc2))

	sc3 := buildConcertCandidate(t, db, artistID)
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

func TestConcertCandidateRepository_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewConcertCandidateRepository(db)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, uuid.NewV7().String())
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

func TestConcertCandidateRepository_ListPendingDedupKeysByArtist(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewConcertCandidateRepository(db)
	ctx := context.Background()

	artistA := seedArtist(t, db, "Artist A", "aaaaaaaa-aaaa-aaaa-aaaa-100000000008")
	artistB := seedArtist(t, db, "Artist B", "aaaaaaaa-aaaa-aaaa-aaaa-100000000009")

	date1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	// Artist A — two concert candidates.
	scA1 := &entity.ConcertCandidate{
		ID: uuid.NewV7().String(), ArtistID: artistA, SeriesID: seedSeriesOnly(t, db, "Series A1"), Title: "Show 1",
		LocalDate: date1, ListedVenueName: "Venue Alpha",
	}
	scA2 := &entity.ConcertCandidate{
		ID: uuid.NewV7().String(), ArtistID: artistA, SeriesID: seedSeriesOnly(t, db, "Series A2"), Title: "Show 2",
		LocalDate: date2, ListedVenueName: "Venue Beta",
	}
	// Artist B — one concert candidate.
	scB1 := &entity.ConcertCandidate{
		ID: uuid.NewV7().String(), ArtistID: artistB, SeriesID: seedSeriesOnly(t, db, "Series B1"), Title: "Show B",
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
