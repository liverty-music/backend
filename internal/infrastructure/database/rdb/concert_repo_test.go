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

// requireCreate wraps ConcertRepository.Create for integration tests that only
// care about the error path. The returned inserted-IDs slice is intentionally
// discarded — tests that assert on the deduplication behaviour should call
// Create directly and inspect the slice.
func requireCreate(t *testing.T, ctx context.Context, repo *rdb.ConcertRepository, concerts ...*entity.Concert) {
	t.Helper()
	_, err := repo.Create(ctx, concerts...)
	require.NoError(t, err)
}

// seedSeries creates a Series row and returns its ID, used to satisfy the
// events.series_id FK before inserting Concert rows in integration tests.
func seedSeries(t *testing.T, ctx context.Context, repo *rdb.SeriesRepository, title string) string {
	t.Helper()
	s := &entity.Series{
		ID:    newTestID(t),
		Title: title,
		Type:  entity.SeriesTypeSingle,
	}
	ids, err := repo.Create(ctx, s)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	return ids[0]
}

// newTestID generates a fresh UUIDv7 string for Series fixtures where a unique
// ID is required but the exact value does not matter for the assertion.
func newTestID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}

func TestConcertRepository_Create(t *testing.T) {
	ctx := context.Background()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	seriesRepo := rdb.NewSeriesRepository(testDB)

	artistID := "018b2f19-e591-7d12-bf9e-f0e74f1b49a1"
	venueID := "018b2f19-e591-7d12-bf9e-f0e74f1b49b1"

	concertDate, _ := time.Parse("2006-01-02", "2026-12-31")
	startTime, _ := time.Parse("15:04", "20:00")
	openTime, _ := time.Parse("15:04", "18:00")

	setupFixtures := func(t *testing.T) {
		t.Helper()
		cleanDatabase(t)
		_, err := artistRepo.Create(ctx, &entity.Artist{ID: artistID, Name: "Concert Test Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49a1"})
		require.NoError(t, err)
		require.NoError(t, venueRepo.Create(ctx, &entity.Venue{ID: venueID, Name: "Concert Test Arena"}))
	}

	t.Run("create valid concert", func(t *testing.T) {
		setupFixtures(t)
		seriesID := seedSeries(t, ctx, seriesRepo, "New Year's Eve Concert")

		_, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c1", VenueID: venueID,
				SeriesID: seriesID, LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			Series:     &entity.Series{ID: seriesID},
			Performers: []*entity.Artist{{ID: artistID}},
		})
		assert.NoError(t, err)
	})

	t.Run("bulk create multiple concerts", func(t *testing.T) {
		setupFixtures(t)
		s1 := seedSeries(t, ctx, seriesRepo, "Bulk Concert 1")
		s2 := seedSeries(t, ctx, seriesRepo, "Bulk Concert 2")
		s3 := seedSeries(t, ctx, seriesRepo, "Bulk Concert 3")

		concerts := []*entity.Concert{
			{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49d1", VenueID: venueID,
					SeriesID: s1, LocalDate: concertDate,
					StartTime: &startTime, OpenTime: &openTime,
				},
				Series:     &entity.Series{ID: s1},
				Performers: []*entity.Artist{{ID: artistID}},
			},
			{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49d2", VenueID: venueID,
					SeriesID: s2, LocalDate: concertDate.AddDate(0, 0, 1),
					StartTime: &startTime, OpenTime: &openTime,
				},
				Series:     &entity.Series{ID: s2},
				Performers: []*entity.Artist{{ID: artistID}},
			},
			{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49d3", VenueID: venueID,
					SeriesID: s3, LocalDate: concertDate.AddDate(0, 0, 2),
					StartTime: &startTime, OpenTime: &openTime,
				},
				Series:     &entity.Series{ID: s3},
				Performers: []*entity.Artist{{ID: artistID}},
			},
		}
		_, err := concertRepo.Create(ctx, concerts...)
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 3)
	})

	t.Run("duplicate concert ID silently skipped", func(t *testing.T) {
		setupFixtures(t)
		seriesID := seedSeries(t, ctx, seriesRepo, "Original")

		concert := &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c1", VenueID: venueID,
				SeriesID: seriesID, LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			Series:     &entity.Series{ID: seriesID},
			Performers: []*entity.Artist{{ID: artistID}},
		}
		_, err := concertRepo.Create(ctx, concert)
		require.NoError(t, err)

		// Same ID again — UPSERT on natural key, concerts ON CONFLICT DO NOTHING.
		_, err = concertRepo.Create(ctx, concert)
		assert.NoError(t, err)
	})

	t.Run("batch with mix of new and duplicate concerts", func(t *testing.T) {
		setupFixtures(t)
		seriesID1 := seedSeries(t, ctx, seriesRepo, "Existing Concert")
		seriesID2 := seedSeries(t, ctx, seriesRepo, "New Concert in Mixed Batch")

		// Seed one concert first.
		requireCreate(t, ctx, concertRepo, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c1", VenueID: venueID,
				SeriesID: seriesID1, LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			Series:     &entity.Series{ID: seriesID1},
			Performers: []*entity.Artist{{ID: artistID}},
		})

		// Batch: one existing (same ID) + one new.
		_, err := concertRepo.Create(ctx,
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c1", VenueID: venueID,
					SeriesID: seriesID1, LocalDate: concertDate,
					StartTime: &startTime, OpenTime: &openTime,
				},
				Series:     &entity.Series{ID: seriesID1},
				Performers: []*entity.Artist{{ID: artistID}},
			},
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49e1", VenueID: venueID,
					SeriesID: seriesID2, LocalDate: concertDate.AddDate(0, 0, 5),
					StartTime: &startTime, OpenTime: &openTime,
				},
				Series:     &entity.Series{ID: seriesID2},
				Performers: []*entity.Artist{{ID: artistID}},
			},
		)
		assert.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("foreign key violation - invalid artist", func(t *testing.T) {
		setupFixtures(t)
		seriesID := seedSeries(t, ctx, seriesRepo, "Invalid Artist Concert")

		_, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c2", VenueID: venueID,
				SeriesID: seriesID, LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			Series:     &entity.Series{ID: seriesID},
			Performers: []*entity.Artist{{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a0"}}, // does not exist
		})
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("foreign key violation - invalid venue", func(t *testing.T) {
		setupFixtures(t)
		seriesID := seedSeries(t, ctx, seriesRepo, "Invalid Venue Concert")

		_, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID:       "018b2f19-e591-7d12-bf9e-f0e74f1b49c3",
				VenueID:  "018b2f19-e591-7d12-bf9e-f0e74f1b49b0", // does not exist
				SeriesID: seriesID, LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			Series:     &entity.Series{ID: seriesID},
			Performers: []*entity.Artist{{ID: artistID}},
		})
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("empty slice - no-op", func(t *testing.T) {
		setupFixtures(t)

		_, err := concertRepo.Create(ctx, []*entity.Concert{}...)
		assert.NoError(t, err)
	})

	t.Run("nil elements are skipped without DB error", func(t *testing.T) {
		setupFixtures(t)
		seriesID := seedSeries(t, ctx, seriesRepo, "Valid Concert Among Nils")

		// Regression: nil elements must be compacted before building unnest arrays.
		// A nil element left at index i results in an empty-string UUID in eventIDs[i],
		// which PostgreSQL rejects as "invalid input syntax for type uuid: """.
		_, err := concertRepo.Create(ctx,
			nil,
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49f1", VenueID: venueID,
					SeriesID: seriesID, LocalDate: concertDate,
					StartTime: &startTime, OpenTime: &openTime,
				},
				Series:     &entity.Series{ID: seriesID},
				Performers: []*entity.Artist{{ID: artistID}},
			},
			nil,
		)
		assert.NoError(t, err)
	})

	// --- UPSERT behaviour ---

	t.Run("natural key conflict — concerts row skipped via WHERE EXISTS", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		seriesID1 := seedSeries(t, ctx, seriesRepo, "Original")
		seriesID2 := seedSeries(t, ctx, seriesRepo, "Duplicate Attempt")

		// First insert: event + concert created normally.
		requireCreate(t, ctx, concertRepo, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c01", VenueID: venueID,
				SeriesID: seriesID1, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime,
			},
			Series:     &entity.Series{ID: seriesID1, SourceURL: "https://example.com/1"},
			Performers: []*entity.Artist{{ID: artistID}},
		})

		// Second insert: same natural key (series_id+venue_id+date) but different UUID.
		// UPSERT updates the existing event row; the input UUID does NOT exist in events,
		// so WHERE EXISTS filters it out and no duplicate concerts row is created.
		_, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c02", VenueID: venueID,
				SeriesID: seriesID1, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime,
			},
			Series:     &entity.Series{ID: seriesID1, SourceURL: "https://example.com/2"},
			Performers: []*entity.Artist{{ID: artistID}},
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 1, "should have exactly 1 concert — duplicate must be skipped")
		require.NotNil(t, got[0].Series)
		assert.Equal(t, "Original", got[0].Series.Title, "original title should be preserved")
		_ = seriesID2 // seeded but unused in this path; kept for clarity
	})

	t.Run("natural key conflict — open_at updated from NULL to non-NULL", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		seriesID := seedSeries(t, ctx, seriesRepo, "No OpenTime")

		// First insert: event with open_at = NULL.
		requireCreate(t, ctx, concertRepo, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c03", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/3"},
			Performers: []*entity.Artist{{ID: artistID}},
		})

		// Second insert: same natural key but open_at is now non-NULL.
		// COALESCE(EXCLUDED.open_at, events.open_at) → fills the NULL.
		_, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c04", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, OpenTime: &openTime,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/4"},
			Performers: []*entity.Artist{{ID: artistID}},
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NotNil(t, got[0].OpenTime, "open_at should be updated from NULL to non-NULL")
	})

	t.Run("natural key conflict — existing non-NULL open_at preserved", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		seriesID := seedSeries(t, ctx, seriesRepo, "Has OpenTime")

		// First insert: event with open_at = non-NULL.
		requireCreate(t, ctx, concertRepo, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c05", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, OpenTime: &openTime,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/5"},
			Performers: []*entity.Artist{{ID: artistID}},
		})

		// Second insert: same natural key but open_at = NULL.
		// COALESCE(NULL, events.open_at) → preserves existing value.
		_, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c06", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/6"},
			Performers: []*entity.Artist{{ID: artistID}},
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NotNil(t, got[0].OpenTime, "existing non-NULL open_at must not be overwritten by NULL")
	})

	t.Run("natural key conflict — existing non-NULL open_at preserved against a different non-NULL incoming value (first-write-wins)", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		seriesID := seedSeries(t, ctx, seriesRepo, "First Write Wins OpenAt")

		// First insert: confirmed open_at.
		requireCreate(t, ctx, concertRepo, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c0b", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, OpenTime: &openTime,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/11"},
			Performers: []*entity.Artist{{ID: artistID}},
		})

		// Second insert: same natural key with a different non-NULL open_at.
		// Symmetric assertion to the start_at first-write-wins test —
		// COALESCE(events.open_at, EXCLUDED.open_at) keeps the first value.
		laterOpen := openTime.Add(2 * time.Hour)
		_, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c0c", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, OpenTime: &laterOpen,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/12"},
			Performers: []*entity.Artist{{ID: artistID}},
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NotNil(t, got[0].OpenTime)
		require.True(t, got[0].OpenTime.Equal(openTime),
			"non-NULL open_at must NOT be overwritten by a different non-NULL incoming value")
	})

	// Note: a "different non-NULL start_at first-write-wins" subtest was removed
	// here — under the physical natural key (venue_id, local_event_date,
	// start_at), two different start times at the same venue/date are DISTINCT
	// events (昼夜2公演), not a conflict. That behaviour is covered by
	// TestConcertRepository_PhysicalNaturalKey; open_at first-write-wins (which
	// is still a genuine same-key conflict) is covered by the open_at subtest above.

	t.Run("co-headliner — second artist's discovery returns the existing event id via linkedEventIDs", func(t *testing.T) {
		// Co-headliner notification path:
		// 1. Artist A is discovered; Create inserts a new event row and
		//    insertConcertsQuery returns its UUID. linkedEventIDs from
		//    insertEventPerformersQuery RETURNING ALSO includes that
		//    same UUID (the new (event, A) link).
		// 2. Artist B is discovered for the same real-world concert.
		//    upsertEventsQuery's natural-key UPSERT keeps the existing
		//    event row, so insertConcertsQuery's WHERE EXISTS filters out
		//    B's phantom input UUID — insertedIDs is empty. But the
		//    natural-key JOIN in insertEventPerformersQuery resolves to
		//    the existing event's UUID and RETURNS it (the new (event, B)
		//    link is genuinely new). The deduped union surfaces that
		//    event for B's followers — without the RETURNING path, B's
		//    followers would never be notified.
		setupFixtures(t)
		artistB := "018b2f19-e591-7d12-bf9e-f0e74f1b49a2"
		_, err := artistRepo.Create(ctx, &entity.Artist{
			ID: artistB, Name: "Co-Headliner B",
			MBID: "bbbbbbbb-bbbb-bbbb-bbbb-f0e74f1b49a2",
		})
		require.NoError(t, err)

		listedVenue := "Zepp DiverCity"
		seriesID := seedSeries(t, ctx, seriesRepo, "Shared Co-Headliner Series")

		// Artist A creates the event.
		aIDs, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c0d", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/coh-a"},
			Performers: []*entity.Artist{{ID: artistID}},
		})
		require.NoError(t, err)
		require.Len(t, aIDs, 1, "artist A's discovery should surface the new event id")

		// Artist B's discovery: same series+venue+date, fresh input UUID
		// for the event, B as the only performer.
		bIDs, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c0e", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/coh-b"},
			Performers: []*entity.Artist{{ID: artistB}},
		})
		require.NoError(t, err)
		require.Len(t, bIDs, 1,
			"artist B's discovery must surface the existing event id so B's followers get notified")
		assert.Equal(t, aIDs[0], bIDs[0],
			"linkedEventIDs must resolve to the pre-existing event row id, not B's phantom input UUID")

		// Verify both artists are now linked to the same event.
		gotA, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		require.Len(t, gotA, 1)
		gotB, err := concertRepo.ListByArtist(ctx, artistB, false)
		require.NoError(t, err)
		require.Len(t, gotB, 1)
		assert.Equal(t, gotA[0].ID, gotB[0].ID,
			"both artists should resolve to the same event row")
	})

	t.Run("NULL start_at — same (series_id, local_event_date, venue_id) triggers UPSERT regardless of start_at", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		seriesID := seedSeries(t, ctx, seriesRepo, "First NULL start")

		// First insert: event with start_at = NULL.
		requireCreate(t, ctx, concertRepo, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c07", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/7"},
			Performers: []*entity.Artist{{ID: artistID}},
		})

		// Second insert: same series+venue+date, also start_at = NULL.
		// Same natural key (series_id, local_event_date, venue_id) → conflict. start_at is NOT part of the key; it's filled in via COALESCE when previously NULL.
		_, err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c08", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/8"},
			Performers: []*entity.Artist{{ID: artistID}},
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 1, "two NULL start_at events with same series+venue+date must conflict")
	})

	t.Run("same artist re-inserted for same event — concerts ON CONFLICT DO NOTHING", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		seriesID := seedSeries(t, ctx, seriesRepo, "Shared Event")
		concert := &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c09", VenueID: venueID,
				SeriesID: seriesID, ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime,
			},
			Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/9"},
			Performers: []*entity.Artist{{ID: artistID}},
		}
		requireCreate(t, ctx, concertRepo, concert)

		// Same event UUID and same artist again.
		// Events UPSERT is a no-op (same id, same natural key).
		// Concerts INSERT hits PK conflict → ON CONFLICT DO NOTHING.
		_, err := concertRepo.Create(ctx, concert)
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 1, "should still have exactly 1 concert — no duplicate")
	})
}

// TestConcertRepository_CoHeadliners verifies the M:N performers contract:
// inserting a Concert with multiple Performers writes one event_performers row
// per artist, and every row round-trips through the hydrate query so callers
// see the full lineup. Covers the "Co-headliner persistence" scenario from the
// event-management spec.
func TestConcertRepository_CoHeadliners(t *testing.T) {
	ctx := context.Background()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	seriesRepo := rdb.NewSeriesRepository(testDB)

	headliner := "018b2f19-e591-7d12-bf9e-f0e74f1bc0a1"
	support := "018b2f19-e591-7d12-bf9e-f0e74f1bc0a2"
	opener := "018b2f19-e591-7d12-bf9e-f0e74f1bc0a3"
	venueID := "018b2f19-e591-7d12-bf9e-f0e74f1bc0b1"
	eventID := "018b2f19-e591-7d12-bf9e-f0e74f1bc0c1"
	concertDate, _ := time.Parse("2006-01-02", "2026-07-04")

	cleanDatabase(t)
	_, err := artistRepo.Create(ctx,
		&entity.Artist{ID: headliner, Name: "Headliner Band", MBID: "11111111-2222-3333-4444-555555555aaa"},
		&entity.Artist{ID: support, Name: "Support Act", MBID: "22222222-3333-4444-5555-666666666bbb"},
		&entity.Artist{ID: opener, Name: "Opening Act", MBID: "33333333-4444-5555-6666-777777777ccc"},
	)
	require.NoError(t, err)
	require.NoError(t, venueRepo.Create(ctx, &entity.Venue{ID: venueID, Name: "Co-Headliner Arena"}))
	seriesID := seedSeries(t, ctx, seriesRepo, "Triple Bill")

	_, err = concertRepo.Create(ctx, &entity.Concert{
		Event:  entity.Event{ID: eventID, SeriesID: seriesID, VenueID: venueID, LocalDate: concertDate},
		Series: &entity.Series{ID: seriesID},
		Performers: []*entity.Artist{
			{ID: headliner},
			{ID: support},
			{ID: opener},
		},
	})
	require.NoError(t, err)

	// Read back via ListByIDs (path used by NotifyNewConcerts) and assert
	// every performer is present.
	got, err := concertRepo.ListByIDs(ctx, []string{eventID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].Performers, 3, "all three M:N rows must round-trip")
	gotIDs := got[0].PerformerIDs()
	assert.ElementsMatch(t, []string{headliner, support, opener}, gotIDs)

	// And via ListByArtist for each performer — every artist should see this
	// concert regardless of billing position.
	for _, aid := range []string{headliner, support, opener} {
		listed, err := concertRepo.ListByArtist(ctx, aid, false)
		require.NoError(t, err)
		require.Len(t, listed, 1, "artist %s should see the shared concert", aid)
		assert.Equal(t, eventID, listed[0].ID)
	}
}

// TestConcertRepository_PhysicalNaturalKey verifies the physical-identity
// natural key (venue_id, local_event_date, start_at): the same physical show
// collapses to one row regardless of series (so a tour-stop and a co-bill
// classification of the same performance do not duplicate), while two shows at
// the same venue and date with different start times stay distinct (昼夜2公演).
// Covers the event-management "Event Natural Key Reflects Physical Identity"
// scenarios.
func TestConcertRepository_PhysicalNaturalKey(t *testing.T) {
	ctx := context.Background()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	seriesRepo := rdb.NewSeriesRepository(testDB)

	artistID := "018b2f19-e591-7d12-bf9e-f0e74f1bd1a1"
	venueID := "018b2f19-e591-7d12-bf9e-f0e74f1bd1b1"
	concertDate, _ := time.Parse("2006-01-02", "2026-08-15")
	matinee := time.Date(2026, 8, 15, 13, 0, 0, 0, time.UTC)
	evening := time.Date(2026, 8, 15, 18, 0, 0, 0, time.UTC)

	cleanDatabase(t)
	_, err := artistRepo.Create(ctx, &entity.Artist{
		ID: artistID, Name: "Shared Artist",
		MBID: "44444444-5555-6666-7777-888888888ddd",
	})
	require.NoError(t, err)
	require.NoError(t, venueRepo.Create(ctx, &entity.Venue{ID: venueID, Name: "Shared Arena"}))

	seriesA := seedSeries(t, ctx, seriesRepo, "Series A")
	seriesB := seedSeries(t, ctx, seriesRepo, "Series B")
	require.NotEqual(t, seriesA, seriesB)

	// Same (venue, date, start) under two different series → one physical event.
	_, err = concertRepo.Create(ctx,
		&entity.Concert{
			Event:      entity.Event{ID: "018b2f19-e591-7d12-bf9e-f0e74f1bd1c1", SeriesID: seriesA, VenueID: venueID, LocalDate: concertDate, StartTime: &evening},
			Series:     &entity.Series{ID: seriesA},
			Performers: []*entity.Artist{{ID: artistID}},
		},
		&entity.Concert{
			Event:      entity.Event{ID: "018b2f19-e591-7d12-bf9e-f0e74f1bd1c2", SeriesID: seriesB, VenueID: venueID, LocalDate: concertDate, StartTime: &evening},
			Series:     &entity.Series{ID: seriesB},
			Performers: []*entity.Artist{{ID: artistID}},
		},
	)
	require.NoError(t, err)

	got, err := concertRepo.ListByArtist(ctx, artistID, false)
	require.NoError(t, err)
	require.Len(t, got, 1, "same venue+date+start collapses to one event regardless of series")

	// A second show at the same venue+date with a different start time (matinee)
	// is a distinct event.
	_, err = concertRepo.Create(ctx, &entity.Concert{
		Event:      entity.Event{ID: "018b2f19-e591-7d12-bf9e-f0e74f1bd1c3", SeriesID: seriesA, VenueID: venueID, LocalDate: concertDate, StartTime: &matinee},
		Series:     &entity.Series{ID: seriesA},
		Performers: []*entity.Artist{{ID: artistID}},
	})
	require.NoError(t, err)

	got, err = concertRepo.ListByArtist(ctx, artistID, false)
	require.NoError(t, err)
	require.Len(t, got, 2, "matinee and evening at the same venue+date are distinct events (昼夜2公演)")
}

// TestConcertRepository_ListedVenueName verifies that ListedVenueName is
// correctly scanned from the database in both the NULL and non-NULL cases.
// This is a regression test for the scan correctness of the nullable column.
func TestConcertRepository_ListedVenueName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	seriesRepo := rdb.NewSeriesRepository(testDB)

	concertDate, _ := time.Parse("2006-01-02", "2026-12-31")
	listedName := "Zepp Nagoya"

	tests := []struct {
		name     string
		setup    func(t *testing.T, artistID, venueID string)
		validate func(t *testing.T, got []*entity.Concert)
		wantErr  error
	}{
		{
			name: "NULL listed_venue_name is scanned to nil without error",
			setup: func(t *testing.T, artistID, venueID string) {
				t.Helper()
				// Insert via the repository without a ListedVenueName to exercise NULL → *string nil mapping.
				seriesID := seedSeries(t, ctx, seriesRepo, "Legacy Concert")
				_, err := concertRepo.Create(ctx, &entity.Concert{
					Event: entity.Event{
						ID:       "018b2f19-e591-7d12-bf9e-f0e74f1b4cc1",
						VenueID:  venueID,
						SeriesID: seriesID,
						// ListedVenueName intentionally omitted → stored as NULL.
						LocalDate: concertDate,
					},
					Series:     &entity.Series{ID: seriesID},
					Performers: []*entity.Artist{{ID: artistID}},
				})
				require.NoError(t, err)
			},
			validate: func(t *testing.T, got []*entity.Concert) {
				t.Helper()
				require.Len(t, got, 1)
				assert.Nil(t, got[0].ListedVenueName, "expected nil for row inserted without listed_venue_name")
			},
			wantErr: nil,
		},
		{
			name: "non-NULL listed_venue_name is persisted and retrieved correctly",
			setup: func(t *testing.T, artistID, venueID string) {
				t.Helper()
				seriesID := seedSeries(t, ctx, seriesRepo, "Modern Concert")
				_, err := concertRepo.Create(ctx, &entity.Concert{
					Event: entity.Event{
						ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4cc2",
						VenueID:         venueID,
						SeriesID:        seriesID,
						ListedVenueName: &listedName,
						LocalDate:       concertDate,
					},
					Series:     &entity.Series{ID: seriesID, SourceURL: "https://example.com/modern"},
					Performers: []*entity.Artist{{ID: artistID}},
				})
				require.NoError(t, err)
			},
			validate: func(t *testing.T, got []*entity.Concert) {
				t.Helper()
				var found *entity.Concert
				for _, c := range got {
					if c.ID == "018b2f19-e591-7d12-bf9e-f0e74f1b4cc2" {
						found = c
						break
					}
				}
				require.NotNil(t, found)
				require.NotNil(t, found.ListedVenueName, "expected non-nil for row with listed_venue_name set")
				assert.Equal(t, listedName, *found.ListedVenueName)
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanDatabase(t)

			artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4aa1", Name: "VenueName Test Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4aa1"}
			_, err := artistRepo.Create(ctx, artist)
			require.NoError(t, err)
			venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4bb1", Name: "VenueName Test Arena"}
			require.NoError(t, venueRepo.Create(ctx, venue))

			tt.setup(t, artist.ID, venue.ID)

			got, err := concertRepo.ListByArtist(ctx, artist.ID, false)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			tt.validate(t, got)
		})
	}
}

func TestConcertRepository_ListByArtist(t *testing.T) {
	ctx := context.Background()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	seriesRepo := rdb.NewSeriesRepository(testDB)

	cleanDatabase(t)

	// Setup: Create test data
	testArtist1 := &entity.Artist{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		Name: "List Test Band 1",
		MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49a2",
	}
	testArtist2 := &entity.Artist{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49a3",
		Name: "List Test Band 2",
		MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49a3",
	}
	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
		Name: "List Test Arena",
	}

	_, err := artistRepo.Create(ctx, testArtist1)
	require.NoError(t, err)
	_, err = artistRepo.Create(ctx, testArtist2)
	require.NoError(t, err)
	err = venueRepo.Create(ctx, testVenue)
	require.NoError(t, err)

	futureDate, _ := time.Parse("2006-01-02", "2026-06-15")
	pastDate, _ := time.Parse("2006-01-02", "2025-01-01")
	startTime, _ := time.Parse("15:04", "20:00")
	openTime, _ := time.Parse("15:04", "18:00")
	startTime2, _ := time.Parse("15:04", "21:00")
	openTime2, _ := time.Parse("15:04", "19:00")

	// Seed series for each concert.
	s1 := seedSeries(t, ctx, seriesRepo, "Concert 1 (future)")
	s2 := seedSeries(t, ctx, seriesRepo, "Concert 2 (future)")
	s3 := seedSeries(t, ctx, seriesRepo, "Concert Past (should be hidden)")
	s4 := seedSeries(t, ctx, seriesRepo, "Concert 3")

	// Create concerts using bulk insert.
	// testArtist1 has: 2 future concerts + 1 past concert (for upcomingOnly testing).
	// testArtist2 has: 1 future concert.
	concerts := []*entity.Concert{
		{
			Event: entity.Event{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c4",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				SeriesID:  s1,
				LocalDate: futureDate,
				StartTime: &startTime,
				OpenTime:  &openTime,
			},
			Series:     &entity.Series{ID: s1, Title: "Concert 1 (future)"},
			Performers: []*entity.Artist{{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2"}},
		},
		{
			Event: entity.Event{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c5",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				SeriesID:  s2,
				LocalDate: futureDate.AddDate(0, 1, 0),
				StartTime: &startTime2,
				OpenTime:  &openTime2,
			},
			Series:     &entity.Series{ID: s2, Title: "Concert 2 (future)"},
			Performers: []*entity.Artist{{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2"}},
		},
		{
			Event: entity.Event{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c7",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				SeriesID:  s3,
				LocalDate: pastDate,
				StartTime: &startTime,
				OpenTime:  &openTime,
			},
			Series:     &entity.Series{ID: s3, Title: "Concert Past (should be hidden)"},
			Performers: []*entity.Artist{{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2"}},
		},
		{
			Event: entity.Event{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c6",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				SeriesID:  s4,
				LocalDate: futureDate,
				StartTime: &startTime2, // different start_time to avoid UPSERT conflict with c4
				OpenTime:  &openTime2,
			},
			Series:     &entity.Series{ID: s4, Title: "Concert 3"},
			Performers: []*entity.Artist{{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a3"}},
		},
	}

	_, err = concertRepo.Create(ctx, concerts...)
	require.NoError(t, err)

	type args struct {
		artistID     string
		upcomingOnly bool
	}

	tests := []struct {
		name string
		args args
		want struct {
			count int
		}
		wantErr  error
		validate func(t *testing.T, concerts []*entity.Concert)
	}{
		{
			name: "list all concerts for artist (upcomingOnly=false returns past and future)",
			args: args{
				artistID:     "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
				upcomingOnly: false,
			},
			want: struct {
				count int
			}{
				count: 3, // 2 future + 1 past
			},
			wantErr: nil,
			validate: func(t *testing.T, concerts []*entity.Concert) {
				for _, c := range concerts {
					require.NotEmpty(t, c.PerformerIDs())
					assert.Equal(t, "018b2f19-e591-7d12-bf9e-f0e74f1b49a2", c.PerformerIDs()[0])
				}
			},
		},
		{
			name: "list upcoming concerts only (upcomingOnly=true filters past concerts)",
			args: args{
				artistID:     "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
				upcomingOnly: true,
			},
			want: struct {
				count int
			}{
				count: 2, // only the 2 future concerts; past concert is excluded
			},
			wantErr: nil,
			validate: func(t *testing.T, concerts []*entity.Concert) {
				for _, c := range concerts {
					require.NotNil(t, c.Series)
					assert.NotEqual(t, "Concert Past (should be hidden)", c.Series.Title, "past concert must not appear when upcomingOnly=true")
				}
			},
		},
		{
			name: "list concerts for artist with 1 concert",
			args: args{
				artistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a3",
			},
			want: struct {
				count int
			}{
				count: 1,
			},
			wantErr: nil,
			validate: func(t *testing.T, concerts []*entity.Concert) {
				require.NotNil(t, concerts[0].Series)
				assert.Equal(t, "Concert 3", concerts[0].Series.Title)
			},
		},
		{
			name: "list concerts for artist with no concerts",
			args: args{
				artistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a0",
			},
			want: struct {
				count int
			}{
				count: 0,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := concertRepo.ListByArtist(ctx, tt.args.artistID, tt.args.upcomingOnly)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.Len(t, got, tt.want.count)
			if tt.validate != nil {
				tt.validate(t, got)
			}
		})
	}

	// Regression tests for ListedVenueName scanning.
	// Pre-migration rows have NULL in listed_venue_name; scanning NULL into a
	// non-pointer string would panic at runtime.
	t.Run("NULL listed_venue_name is scanned to nil without error", func(t *testing.T) {
		cleanDatabase(t)

		artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4aa1", Name: "VenueName Test Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4aa1"}
		_, err := artistRepo.Create(ctx, artist)
		require.NoError(t, err)
		venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4bb1", Name: "VenueName Test Arena"}
		require.NoError(t, venueRepo.Create(ctx, venue))

		concertDate, _ := time.Parse("2006-01-02", "2026-12-31")

		// Insert via repository without listed_venue_name.
		sid := seedSeries(t, ctx, seriesRepo, "Legacy Concert")
		_, err = concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID:       "018b2f19-e591-7d12-bf9e-f0e74f1b4cc1",
				VenueID:  venue.ID,
				SeriesID: sid,
				// ListedVenueName intentionally omitted → stored as NULL.
				LocalDate: concertDate,
			},
			Series:     &entity.Series{ID: sid},
			Performers: []*entity.Artist{{ID: artist.ID}},
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artist.ID, false)
		assert.NoError(t, err)
		require.Len(t, got, 1)
		assert.Nil(t, got[0].ListedVenueName, "expected nil for pre-migration NULL row")
	})

	t.Run("non-NULL listed_venue_name is persisted and retrieved correctly", func(t *testing.T) {
		cleanDatabase(t)

		artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4aa1", Name: "VenueName Test Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4aa1"}
		_, err := artistRepo.Create(ctx, artist)
		require.NoError(t, err)
		venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4bb1", Name: "VenueName Test Arena"}
		require.NoError(t, venueRepo.Create(ctx, venue))

		concertDate, _ := time.Parse("2006-01-02", "2026-12-31")

		listedName := "Zepp Nagoya"
		sid := seedSeries(t, ctx, seriesRepo, "Modern Concert")
		_, err = concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4cc2",
				VenueID:         venue.ID,
				SeriesID:        sid,
				ListedVenueName: &listedName,
				LocalDate:       concertDate,
			},
			Series:     &entity.Series{ID: sid, SourceURL: "https://example.com/modern"},
			Performers: []*entity.Artist{{ID: artist.ID}},
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artist.ID, false)
		assert.NoError(t, err)
		require.Len(t, got, 1)
		require.NotNil(t, got[0].ListedVenueName, "expected non-nil for row with listed_venue_name set")
		assert.Equal(t, listedName, *got[0].ListedVenueName)
	})
}

func TestConcertRepository_ListByArtists(t *testing.T) {
	ctx := context.Background()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	seriesRepo := rdb.NewSeriesRepository(testDB)

	t.Run("returns concerts for multiple artists with coordinates", func(t *testing.T) {
		cleanDatabase(t)

		artist1 := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6001", Name: "Multi Band 1", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b6001"}
		artist2 := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6002", Name: "Multi Band 2", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b6002"}
		_, err := artistRepo.Create(ctx, artist1)
		require.NoError(t, err)
		_, err = artistRepo.Create(ctx, artist2)
		require.NoError(t, err)

		venue := &entity.Venue{
			ID:          "018b2f19-e591-7d12-bf9e-f0e74f1b6011",
			Name:        "Multi Venue",
			Coordinates: &entity.Coordinates{Latitude: 33.5904, Longitude: 130.4017},
		}
		require.NoError(t, venueRepo.Create(ctx, venue))

		concertDate, _ := time.Parse("2006-01-02", "2026-10-01")
		startTime, _ := time.Parse("15:04", "19:00")

		s1 := seedSeries(t, ctx, seriesRepo, "Multi Concert 1")
		s2 := seedSeries(t, ctx, seriesRepo, "Multi Concert 2")

		requireCreate(t, ctx, concertRepo,
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6021", VenueID: venue.ID,
					SeriesID: s1, LocalDate: concertDate, StartTime: &startTime,
				},
				Series:     &entity.Series{ID: s1, Title: "Multi Concert 1"},
				Performers: []*entity.Artist{{ID: artist1.ID}},
			},
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6022", VenueID: venue.ID,
					SeriesID: s2, LocalDate: concertDate.AddDate(0, 0, 1), StartTime: &startTime,
				},
				Series:     &entity.Series{ID: s2, Title: "Multi Concert 2"},
				Performers: []*entity.Artist{{ID: artist2.ID}},
			},
		)

		got, err := concertRepo.ListByArtists(ctx, []string{artist1.ID, artist2.ID})
		require.NoError(t, err)
		assert.Len(t, got, 2)

		// Verify date ordering (ASC)
		require.NotNil(t, got[0].Series)
		require.NotNil(t, got[1].Series)
		assert.Equal(t, "Multi Concert 1", got[0].Series.Title)
		assert.Equal(t, "Multi Concert 2", got[1].Series.Title)

		// Verify venue coordinates are populated
		for _, c := range got {
			require.NotNil(t, c.Venue)
			require.NotNil(t, c.Venue.Coordinates, "ListByArtists must include venue coordinates")
			assert.InDelta(t, 33.5904, c.Venue.Coordinates.Latitude, 0.0001)
			assert.InDelta(t, 130.4017, c.Venue.Coordinates.Longitude, 0.0001)
		}
	})

	t.Run("returns empty list for unknown artist IDs", func(t *testing.T) {
		cleanDatabase(t)

		got, err := concertRepo.ListByArtists(ctx, []string{"018b2f19-e591-7d12-bf9e-f0e74f1b6099"})
		assert.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("venue without coordinates returns nil Coordinates", func(t *testing.T) {
		cleanDatabase(t)

		artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6003", Name: "No Coord Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b6003"}
		_, err := artistRepo.Create(ctx, artist)
		require.NoError(t, err)

		venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6012", Name: "No Coord Venue"}
		require.NoError(t, venueRepo.Create(ctx, venue))

		concertDate, _ := time.Parse("2006-01-02", "2026-11-01")
		sid := seedSeries(t, ctx, seriesRepo, "No Coord Concert")
		requireCreate(t, ctx, concertRepo, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6023", VenueID: venue.ID,
				SeriesID: sid, LocalDate: concertDate,
			},
			Series:     &entity.Series{ID: sid},
			Performers: []*entity.Artist{{ID: artist.ID}},
		})

		got, err := concertRepo.ListByArtists(ctx, []string{artist.ID})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NotNil(t, got[0].Venue)
		assert.Nil(t, got[0].Venue.Coordinates, "venue without lat/lng should have nil Coordinates")
	})
}

func TestConcertRepository_ListByFollower(t *testing.T) {
	ctx := context.Background()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	seriesRepo := rdb.NewSeriesRepository(testDB)

	t.Run("returns concerts for followed artists", func(t *testing.T) {
		cleanDatabase(t)

		// Setup: user, 2 artists, venue, concerts, follow relationships
		userID := "018b2f19-e591-7d12-bf9e-f0e74f1b5001"
		_, err := testDB.Pool.Exec(ctx,
			"INSERT INTO users (id, name, email, external_id) VALUES ($1, $2, $3, $4)",
			userID, "Test User", "follower@test.com", "ext-user-001",
		)
		require.NoError(t, err)

		artist1 := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5011", Name: "Followed Band 1", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b5011"}
		artist2 := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5012", Name: "Unfollowed Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b5012"}
		_, err = artistRepo.Create(ctx, artist1)
		require.NoError(t, err)
		_, err = artistRepo.Create(ctx, artist2)
		require.NoError(t, err)

		venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5021", Name: "Follower Test Venue"}
		require.NoError(t, venueRepo.Create(ctx, venue))

		concertDate, _ := time.Parse("2006-01-02", "2026-08-01")
		startTime, _ := time.Parse("15:04", "19:00")

		s1 := seedSeries(t, ctx, seriesRepo, "Followed Concert 1")
		s2 := seedSeries(t, ctx, seriesRepo, "Unfollowed Concert")

		// Create concerts for both artists
		requireCreate(t, ctx, concertRepo,
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5031", VenueID: venue.ID,
					SeriesID: s1, LocalDate: concertDate, StartTime: &startTime,
				},
				Series:     &entity.Series{ID: s1, Title: "Followed Concert 1"},
				Performers: []*entity.Artist{{ID: artist1.ID}},
			},
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5032", VenueID: venue.ID,
					SeriesID: s2, LocalDate: concertDate.AddDate(0, 0, 1), StartTime: &startTime,
				},
				Series:     &entity.Series{ID: s2, Title: "Unfollowed Concert"},
				Performers: []*entity.Artist{{ID: artist2.ID}},
			},
		)

		// Follow only artist1
		_, err = testDB.Pool.Exec(ctx,
			"INSERT INTO followed_artists (user_id, artist_id) VALUES ($1, $2)",
			userID, artist1.ID,
		)
		require.NoError(t, err)

		got, err := concertRepo.ListByFollower(ctx, userID)
		assert.NoError(t, err)
		require.Len(t, got, 1, "should only return concerts for followed artists")
		require.NotNil(t, got[0].Series)
		assert.Equal(t, "Followed Concert 1", got[0].Series.Title)
		assert.NotNil(t, got[0].Venue, "venue should be populated")
		assert.Equal(t, "Follower Test Venue", got[0].Venue.Name)
		assert.Nil(t, got[0].Venue.Coordinates, "venue without lat/lng should have nil Coordinates")
	})

	t.Run("returns venue Coordinates when DB has lat/lng", func(t *testing.T) {
		cleanDatabase(t)

		userID := "018b2f19-e591-7d12-bf9e-f0e74f1b5003"
		_, err := testDB.Pool.Exec(ctx,
			"INSERT INTO users (id, name, email, external_id) VALUES ($1, $2, $3, $4)",
			userID, "Coord User", "coord@test.com", "ext-user-003",
		)
		require.NoError(t, err)

		artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5013", Name: "Coord Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b5013"}
		_, err = artistRepo.Create(ctx, artist)
		require.NoError(t, err)

		venue := &entity.Venue{
			ID:          "018b2f19-e591-7d12-bf9e-f0e74f1b5023",
			Name:        "Enriched Venue",
			Coordinates: &entity.Coordinates{Latitude: 35.6894, Longitude: 139.6917},
		}
		require.NoError(t, venueRepo.Create(ctx, venue))

		concertDate, _ := time.Parse("2006-01-02", "2026-09-01")
		sid := seedSeries(t, ctx, seriesRepo, "Coord Concert")
		requireCreate(t, ctx, concertRepo, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5041", VenueID: venue.ID,
				SeriesID: sid, LocalDate: concertDate,
			},
			Series:     &entity.Series{ID: sid},
			Performers: []*entity.Artist{{ID: artist.ID}},
		})

		_, err = testDB.Pool.Exec(ctx,
			"INSERT INTO followed_artists (user_id, artist_id) VALUES ($1, $2)",
			userID, artist.ID,
		)
		require.NoError(t, err)

		got, err := concertRepo.ListByFollower(ctx, userID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NotNil(t, got[0].Venue)
		require.NotNil(t, got[0].Venue.Coordinates, "venue with lat/lng should have non-nil Coordinates")
		assert.InDelta(t, 35.6894, got[0].Venue.Coordinates.Latitude, 0.0001)
		assert.InDelta(t, 139.6917, got[0].Venue.Coordinates.Longitude, 0.0001)
	})

	t.Run("returns empty list when no followed artists", func(t *testing.T) {
		cleanDatabase(t)

		userID := "018b2f19-e591-7d12-bf9e-f0e74f1b5002"
		_, err := testDB.Pool.Exec(ctx,
			"INSERT INTO users (id, name, email, external_id) VALUES ($1, $2, $3, $4)",
			userID, "Lonely User", "lonely@test.com", "ext-user-002",
		)
		require.NoError(t, err)

		got, err := concertRepo.ListByFollower(ctx, userID)
		assert.NoError(t, err)
		assert.Empty(t, got)
	})
}
