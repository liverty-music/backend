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

func TestConcertRepository_Create(t *testing.T) {
	ctx := context.Background()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)

	artistID := "018b2f19-e591-7d12-bf9e-f0e74f1b49a1"
	venueID := "018b2f19-e591-7d12-bf9e-f0e74f1b49b1"

	concertDate, _ := time.Parse("2006-01-02", "2026-12-31")
	startTime, _ := time.Parse("15:04", "20:00")
	openTime, _ := time.Parse("15:04", "18:00")

	setupFixtures := func(t *testing.T) {
		t.Helper()
		cleanDatabase()
		_, err := artistRepo.Create(ctx, &entity.Artist{ID: artistID, Name: "Concert Test Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b49a1"})
		require.NoError(t, err)
		require.NoError(t, venueRepo.Create(ctx, &entity.Venue{ID: venueID, Name: "Concert Test Arena"}))
	}

	t.Run("create valid concert", func(t *testing.T) {
		setupFixtures(t)

		err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c1", VenueID: venueID,
				Title: "New Year's Eve Concert", LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			ArtistID: artistID,
		})
		assert.NoError(t, err)
	})

	t.Run("bulk create multiple concerts", func(t *testing.T) {
		setupFixtures(t)

		concerts := []*entity.Concert{
			{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49d1", VenueID: venueID,
					Title: "Bulk Concert 1", LocalDate: concertDate,
					StartTime: &startTime, OpenTime: &openTime,
				},
				ArtistID: artistID,
			},
			{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49d2", VenueID: venueID,
					Title: "Bulk Concert 2", LocalDate: concertDate.AddDate(0, 0, 1),
					StartTime: &startTime, OpenTime: &openTime,
				},
				ArtistID: artistID,
			},
			{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49d3", VenueID: venueID,
					Title: "Bulk Concert 3", LocalDate: concertDate.AddDate(0, 0, 2),
					StartTime: &startTime, OpenTime: &openTime,
				},
				ArtistID: artistID,
			},
		}
		err := concertRepo.Create(ctx, concerts...)
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 3)
	})

	t.Run("duplicate concert ID silently skipped", func(t *testing.T) {
		setupFixtures(t)

		concert := &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c1", VenueID: venueID,
				Title: "Original", LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			ArtistID: artistID,
		}
		require.NoError(t, concertRepo.Create(ctx, concert))

		// Same ID again — UPSERT on natural key, concerts ON CONFLICT DO NOTHING.
		err := concertRepo.Create(ctx, concert)
		assert.NoError(t, err)
	})

	t.Run("batch with mix of new and duplicate concerts", func(t *testing.T) {
		setupFixtures(t)

		// Seed one concert first.
		require.NoError(t, concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c1", VenueID: venueID,
				Title: "Existing Concert", LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			ArtistID: artistID,
		}))

		// Batch: one existing (same ID) + one new.
		err := concertRepo.Create(ctx,
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c1", VenueID: venueID,
					Title: "Existing Concert", LocalDate: concertDate,
					StartTime: &startTime, OpenTime: &openTime,
				},
				ArtistID: artistID,
			},
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49e1", VenueID: venueID,
					Title: "New Concert in Mixed Batch", LocalDate: concertDate.AddDate(0, 0, 5),
					StartTime: &startTime, OpenTime: &openTime,
				},
				ArtistID: artistID,
			},
		)
		assert.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("foreign key violation - invalid artist", func(t *testing.T) {
		setupFixtures(t)

		err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49c2", VenueID: venueID,
				Title: "Invalid Artist Concert", LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a0", // does not exist
		})
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("foreign key violation - invalid venue", func(t *testing.T) {
		setupFixtures(t)

		err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID:      "018b2f19-e591-7d12-bf9e-f0e74f1b49c3",
				VenueID: "018b2f19-e591-7d12-bf9e-f0e74f1b49b0", // does not exist
				Title:   "Invalid Venue Concert", LocalDate: concertDate,
				StartTime: &startTime, OpenTime: &openTime,
			},
			ArtistID: artistID,
		})
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("empty slice - no-op", func(t *testing.T) {
		setupFixtures(t)

		err := concertRepo.Create(ctx, []*entity.Concert{}...)
		assert.NoError(t, err)
	})

	t.Run("nil elements are skipped without DB error", func(t *testing.T) {
		setupFixtures(t)

		// Regression: nil elements must be compacted before building unnest arrays.
		// A nil element left at index i results in an empty-string UUID in eventIDs[i],
		// which PostgreSQL rejects as "invalid input syntax for type uuid: """.
		err := concertRepo.Create(ctx,
			nil,
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b49f1", VenueID: venueID,
					Title: "Valid Concert Among Nils", LocalDate: concertDate,
					StartTime: &startTime, OpenTime: &openTime,
				},
				ArtistID: artistID,
			},
			nil,
		)
		assert.NoError(t, err)
	})

	// --- UPSERT behaviour ---

	t.Run("natural key conflict — concerts row skipped via WHERE EXISTS", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		// First insert: event + concert created normally.
		require.NoError(t, concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c01", VenueID: venueID,
				Title: "Original", ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, SourceURL: "https://example.com/1",
			},
			ArtistID: artistID,
		}))

		// Second insert: same natural key (artist_id, date) but different UUID.
		// UPSERT updates the existing event row; the input UUID does NOT exist in events,
		// so WHERE EXISTS filters it out and no duplicate concerts row is created.
		err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c02", VenueID: venueID,
				Title: "Duplicate Attempt", ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, SourceURL: "https://example.com/2",
			},
			ArtistID: artistID,
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 1, "should have exactly 1 concert — duplicate must be skipped")
		assert.Equal(t, "Original", got[0].Title, "original title should be preserved")
	})

	t.Run("natural key conflict — open_at updated from NULL to non-NULL", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		// First insert: event with open_at = NULL.
		require.NoError(t, concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c03", VenueID: venueID,
				Title: "No OpenTime", ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, SourceURL: "https://example.com/3",
			},
			ArtistID: artistID,
		}))

		// Second insert: same natural key but open_at is now non-NULL.
		// COALESCE(EXCLUDED.open_at, events.open_at) → fills the NULL.
		err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c04", VenueID: venueID,
				Title: "With OpenTime", ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, OpenTime: &openTime,
				SourceURL: "https://example.com/4",
			},
			ArtistID: artistID,
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
		// First insert: event with open_at = non-NULL.
		require.NoError(t, concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c05", VenueID: venueID,
				Title: "Has OpenTime", ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, OpenTime: &openTime,
				SourceURL: "https://example.com/5",
			},
			ArtistID: artistID,
		}))

		// Second insert: same natural key but open_at = NULL.
		// COALESCE(NULL, events.open_at) → preserves existing value.
		err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c06", VenueID: venueID,
				Title: "Missing OpenTime", ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime,
				SourceURL: "https://example.com/6",
			},
			ArtistID: artistID,
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NotNil(t, got[0].OpenTime, "existing non-NULL open_at must not be overwritten by NULL")
	})

	t.Run("NULL start_at conflict — NULLS NOT DISTINCT triggers UPSERT", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		// First insert: event with start_at = NULL.
		require.NoError(t, concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c07", VenueID: venueID,
				Title: "First NULL start", ListedVenueName: &listedVenue,
				LocalDate: concertDate, SourceURL: "https://example.com/7",
			},
			ArtistID: artistID,
		}))

		// Second insert: same artist+date, also start_at = NULL.
		// NULLS NOT DISTINCT means (artist, date, NULL) == (artist, date, NULL) → conflict.
		err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c08", VenueID: venueID,
				Title: "Second NULL start", ListedVenueName: &listedVenue,
				LocalDate: concertDate, SourceURL: "https://example.com/8",
			},
			ArtistID: artistID,
		})
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 1, "two NULL start_at events with same artist+date must conflict")
	})

	t.Run("same artist re-inserted for same event — concerts ON CONFLICT DO NOTHING", func(t *testing.T) {
		setupFixtures(t)

		listedVenue := "Zepp DiverCity"
		concert := &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6c09", VenueID: venueID,
				Title: "Shared Event", ListedVenueName: &listedVenue,
				LocalDate: concertDate, StartTime: &startTime, SourceURL: "https://example.com/9",
			},
			ArtistID: artistID,
		}
		require.NoError(t, concertRepo.Create(ctx, concert))

		// Same event UUID and same artist again.
		// Events UPSERT is a no-op (same id, same natural key).
		// Concerts INSERT hits PK conflict → ON CONFLICT DO NOTHING.
		err := concertRepo.Create(ctx, concert)
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artistID, false)
		require.NoError(t, err)
		assert.Len(t, got, 1, "should still have exactly 1 concert — no duplicate")
	})
}

// TestConcertRepository_ListedVenueName verifies that ListedVenueName is
// correctly scanned from the database in both the NULL and non-NULL cases.
// This is a regression test for the pre-migration NULL scan bug: rows inserted
// before the listed_venue_name column was added have NULL in that column, and
// scanning NULL into a non-pointer string would panic at runtime.
func TestConcertRepository_ListedVenueName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)

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
				// Insert directly without listed_venue_name to exercise NULL → *string nil mapping.
				_, err := testDB.Pool.Exec(ctx,
					"INSERT INTO events (id, venue_id, title, local_event_date, source_url, artist_id) VALUES ($1, $2, $3, $4, $5, $6)",
					"018b2f19-e591-7d12-bf9e-f0e74f1b4cc1", venueID, "Legacy Concert", concertDate, "https://example.com/legacy", artistID,
				)
				require.NoError(t, err)
				_, err = testDB.Pool.Exec(ctx,
					"INSERT INTO concerts (event_id, artist_id) VALUES ($1, $2)",
					"018b2f19-e591-7d12-bf9e-f0e74f1b4cc1", artistID,
				)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, got []*entity.Concert) {
				t.Helper()
				require.Len(t, got, 1)
				assert.Nil(t, got[0].ListedVenueName, "expected nil for pre-migration NULL row")
			},
			wantErr: nil,
		},
		{
			name: "non-NULL listed_venue_name is persisted and retrieved correctly",
			setup: func(t *testing.T, artistID, venueID string) {
				t.Helper()
				err := concertRepo.Create(ctx, &entity.Concert{
					Event: entity.Event{
						ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4cc2",
						VenueID:         venueID,
						Title:           "Modern Concert",
						ListedVenueName: &listedName,
						LocalDate:       concertDate,
						SourceURL:       "https://example.com/modern",
					},
					ArtistID: artistID,
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
			cleanDatabase()

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

	cleanDatabase()

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

	// Create concerts using bulk insert.
	// testArtist1 has: 2 future concerts + 1 past concert (for upcomingOnly testing).
	// testArtist2 has: 1 future concert.
	concerts := []*entity.Concert{
		{
			Event: entity.Event{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c4",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:     "Concert 1 (future)",
				LocalDate: futureDate,
				StartTime: &startTime,
				OpenTime:  &openTime,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		},
		{
			Event: entity.Event{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c5",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:     "Concert 2 (future)",
				LocalDate: futureDate.AddDate(0, 1, 0),
				StartTime: &startTime2,
				OpenTime:  &openTime2,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		},
		{
			Event: entity.Event{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c7",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:     "Concert Past (should be hidden)",
				LocalDate: pastDate,
				StartTime: &startTime,
				OpenTime:  &openTime,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		},
		{
			Event: entity.Event{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c6",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:     "Concert 3",
				LocalDate: futureDate,
				StartTime: &startTime2, // different start_time to avoid UPSERT conflict with c4
				OpenTime:  &openTime2,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a3",
		},
	}

	err = concertRepo.Create(ctx, concerts...)
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
					assert.Equal(t, "018b2f19-e591-7d12-bf9e-f0e74f1b49a2", c.ArtistID)
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
					assert.NotEqual(t, "Concert Past (should be hidden)", c.Title, "past concert must not appear when upcomingOnly=true")
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
				assert.Equal(t, "Concert 3", concerts[0].Title)
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
		cleanDatabase()

		artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4aa1", Name: "VenueName Test Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4aa1"}
		_, err := artistRepo.Create(ctx, artist)
		require.NoError(t, err)
		venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4bb1", Name: "VenueName Test Arena"}
		require.NoError(t, venueRepo.Create(ctx, venue))

		concertDate, _ := time.Parse("2006-01-02", "2026-12-31")
		// Insert directly without listed_venue_name to exercise NULL → *string nil mapping.
		_, err = testDB.Pool.Exec(ctx,
			"INSERT INTO events (id, venue_id, title, local_event_date, source_url, artist_id) VALUES ($1, $2, $3, $4, $5, $6)",
			"018b2f19-e591-7d12-bf9e-f0e74f1b4cc1", venue.ID, "Legacy Concert", concertDate, "https://example.com/legacy", artist.ID,
		)
		require.NoError(t, err)
		_, err = testDB.Pool.Exec(ctx,
			"INSERT INTO concerts (event_id, artist_id) VALUES ($1, $2)",
			"018b2f19-e591-7d12-bf9e-f0e74f1b4cc1", artist.ID,
		)
		require.NoError(t, err)

		got, err := concertRepo.ListByArtist(ctx, artist.ID, false)
		assert.NoError(t, err)
		require.Len(t, got, 1)
		assert.Nil(t, got[0].ListedVenueName, "expected nil for pre-migration NULL row")
	})

	t.Run("non-NULL listed_venue_name is persisted and retrieved correctly", func(t *testing.T) {
		cleanDatabase()

		artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4aa1", Name: "VenueName Test Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b4aa1"}
		_, err := artistRepo.Create(ctx, artist)
		require.NoError(t, err)
		venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4bb1", Name: "VenueName Test Arena"}
		require.NoError(t, venueRepo.Create(ctx, venue))

		concertDate, _ := time.Parse("2006-01-02", "2026-12-31")

		listedName := "Zepp Nagoya"
		err = concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4cc2",
				VenueID:         venue.ID,
				Title:           "Modern Concert",
				ListedVenueName: &listedName,
				LocalDate:       concertDate,
				SourceURL:       "https://example.com/modern",
			},
			ArtistID: artist.ID,
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

	t.Run("returns concerts for multiple artists with coordinates", func(t *testing.T) {
		cleanDatabase()

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

		require.NoError(t, concertRepo.Create(ctx,
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6021", VenueID: venue.ID,
					Title: "Multi Concert 1", LocalDate: concertDate, StartTime: &startTime,
				},
				ArtistID: artist1.ID,
			},
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6022", VenueID: venue.ID,
					Title: "Multi Concert 2", LocalDate: concertDate.AddDate(0, 0, 1), StartTime: &startTime,
				},
				ArtistID: artist2.ID,
			},
		))

		got, err := concertRepo.ListByArtists(ctx, []string{artist1.ID, artist2.ID})
		require.NoError(t, err)
		assert.Len(t, got, 2)

		// Verify date ordering (ASC)
		assert.Equal(t, "Multi Concert 1", got[0].Title)
		assert.Equal(t, "Multi Concert 2", got[1].Title)

		// Verify venue coordinates are populated
		for _, c := range got {
			require.NotNil(t, c.Venue)
			require.NotNil(t, c.Venue.Coordinates, "ListByArtists must include venue coordinates")
			assert.InDelta(t, 33.5904, c.Venue.Coordinates.Latitude, 0.0001)
			assert.InDelta(t, 130.4017, c.Venue.Coordinates.Longitude, 0.0001)
		}
	})

	t.Run("returns empty list for unknown artist IDs", func(t *testing.T) {
		cleanDatabase()

		got, err := concertRepo.ListByArtists(ctx, []string{"018b2f19-e591-7d12-bf9e-f0e74f1b6099"})
		assert.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("venue without coordinates returns nil Coordinates", func(t *testing.T) {
		cleanDatabase()

		artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6003", Name: "No Coord Band", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-f0e74f1b6003"}
		_, err := artistRepo.Create(ctx, artist)
		require.NoError(t, err)

		venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6012", Name: "No Coord Venue"}
		require.NoError(t, venueRepo.Create(ctx, venue))

		concertDate, _ := time.Parse("2006-01-02", "2026-11-01")
		require.NoError(t, concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b6023", VenueID: venue.ID,
				Title: "No Coord Concert", LocalDate: concertDate,
			},
			ArtistID: artist.ID,
		}))

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

	t.Run("returns concerts for followed artists", func(t *testing.T) {
		cleanDatabase()

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

		// Create concerts for both artists
		require.NoError(t, concertRepo.Create(ctx,
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5031", VenueID: venue.ID,
					Title: "Followed Concert 1", LocalDate: concertDate, StartTime: &startTime,
				},
				ArtistID: artist1.ID,
			},
			&entity.Concert{
				Event: entity.Event{
					ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5032", VenueID: venue.ID,
					Title: "Unfollowed Concert", LocalDate: concertDate.AddDate(0, 0, 1), StartTime: &startTime,
				},
				ArtistID: artist2.ID,
			},
		))

		// Follow only artist1
		_, err = testDB.Pool.Exec(ctx,
			"INSERT INTO followed_artists (user_id, artist_id) VALUES ($1, $2)",
			userID, artist1.ID,
		)
		require.NoError(t, err)

		got, err := concertRepo.ListByFollower(ctx, userID)
		assert.NoError(t, err)
		require.Len(t, got, 1, "should only return concerts for followed artists")
		assert.Equal(t, "Followed Concert 1", got[0].Title)
		assert.NotNil(t, got[0].Venue, "venue should be populated")
		assert.Equal(t, "Follower Test Venue", got[0].Venue.Name)
		assert.Nil(t, got[0].Venue.Coordinates, "venue without lat/lng should have nil Coordinates")
	})

	t.Run("returns venue Coordinates when DB has lat/lng", func(t *testing.T) {
		cleanDatabase()

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
		require.NoError(t, concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID: "018b2f19-e591-7d12-bf9e-f0e74f1b5041", VenueID: venue.ID,
				Title: "Coord Concert", LocalDate: concertDate,
			},
			ArtistID: artist.ID,
		}))

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
		cleanDatabase()

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
