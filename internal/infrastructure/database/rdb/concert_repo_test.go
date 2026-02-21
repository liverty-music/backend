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
	cleanDatabase()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Setup: Create test artist and venue
	testArtist := &entity.Artist{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
		Name: "Concert Test Band",
	}
	_, err := artistRepo.Create(ctx, testArtist)
	require.NoError(t, err)

	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
		Name: "Concert Test Arena",
	}
	err = venueRepo.Create(ctx, testVenue)
	require.NoError(t, err)

	concertDate, _ := time.Parse("2006-01-02", "2026-12-31")
	startTime, _ := time.Parse("15:04", "20:00")
	openTime, _ := time.Parse("15:04", "18:00")

	type args struct {
		concerts []*entity.Concert
	}

	tests := []struct {
		name    string
		args    args
		wantErr error
	}{
		{
			name: "create valid concert",
			args: args{
				concerts: []*entity.Concert{
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c1",
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
							Title:          "New Year's Eve Concert",
							LocalEventDate: concertDate,
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "bulk create multiple concerts",
			args: args{
				concerts: []*entity.Concert{
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49d1",
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
							Title:          "Bulk Concert 1",
							LocalEventDate: concertDate,
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					},
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49d2",
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
							Title:          "Bulk Concert 2",
							LocalEventDate: concertDate.AddDate(0, 0, 1),
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					},
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49d3",
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
							Title:          "Bulk Concert 3",
							LocalEventDate: concertDate.AddDate(0, 0, 2),
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "duplicate concert ID silently skipped",
			args: args{
				concerts: []*entity.Concert{
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c1",
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
							Title:          "Duplicate Concert",
							LocalEventDate: concertDate,
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "batch with mix of new and duplicate concerts",
			args: args{
				concerts: []*entity.Concert{
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c1", // already exists
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
							Title:          "Existing Concert",
							LocalEventDate: concertDate,
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					},
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49e1", // new
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
							Title:          "New Concert in Mixed Batch",
							LocalEventDate: concertDate.AddDate(0, 0, 5),
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "foreign key violation - invalid artist",
			args: args{
				concerts: []*entity.Concert{
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c2",
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
							Title:          "Invalid Artist Concert",
							LocalEventDate: concertDate,
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a0",
					},
				},
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "foreign key violation - invalid venue",
			args: args{
				concerts: []*entity.Concert{
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c3",
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b0",
							Title:          "Invalid Venue Concert",
							LocalEventDate: concertDate,
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					},
				},
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "empty slice - no-op",
			args: args{
				concerts: []*entity.Concert{},
			},
			wantErr: nil,
		},
		{
			// Regression: nil elements must be compacted before building unnest arrays.
			// A nil element left at index i results in an empty-string UUID in eventIDs[i],
			// which PostgreSQL rejects as "invalid input syntax for type uuid: """.
			name: "nil elements are skipped without DB error",
			args: args{
				concerts: []*entity.Concert{
					nil,
					{
						Event: entity.Event{
							ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49f1",
							VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
							Title:          "Valid Concert Among Nils",
							LocalEventDate: concertDate,
							StartTime:      &startTime,
							OpenTime:       &openTime,
						},
						ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					},
					nil,
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := concertRepo.Create(ctx, tt.args.concerts...)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)

			// Verify bulk insert actually created all concerts
			if tt.name == "bulk create multiple concerts" {
				for _, concert := range tt.args.concerts {
					concerts, err := concertRepo.ListByArtist(ctx, concert.ArtistID, false)
					require.NoError(t, err)
					assert.GreaterOrEqual(t, len(concerts), 3)
				}
			}
		})
	}
}

func TestConcertRepository_ListByArtist(t *testing.T) {
	cleanDatabase()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Setup: Create test data
	testArtist1 := &entity.Artist{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		Name: "List Test Band 1",
	}
	testArtist2 := &entity.Artist{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49a3",
		Name: "List Test Band 2",
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
				ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c4",
				VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:          "Concert 1 (future)",
				LocalEventDate: futureDate,
				StartTime:      &startTime,
				OpenTime:       &openTime,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		},
		{
			Event: entity.Event{
				ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c5",
				VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:          "Concert 2 (future)",
				LocalEventDate: futureDate.AddDate(0, 1, 0),
				StartTime:      &startTime2,
				OpenTime:       &openTime2,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		},
		{
			Event: entity.Event{
				ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c7",
				VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:          "Concert Past (should be hidden)",
				LocalEventDate: pastDate,
				StartTime:      &startTime,
				OpenTime:       &openTime,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		},
		{
			Event: entity.Event{
				ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c6",
				VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:          "Concert 3",
				LocalEventDate: futureDate,
				StartTime:      &startTime,
				OpenTime:       &openTime,
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
	t.Run("NULL listed_venue_name (pre-migration row) is scanned to nil without error", func(t *testing.T) {
		cleanDatabase()

		artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4aa1", Name: "VenueName Test Band"}
		require.NoError(t, artistRepo.Create(ctx, artist))
		venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4bb1", Name: "VenueName Test Arena"}
		require.NoError(t, venueRepo.Create(ctx, venue))

		// Simulate a pre-migration row by inserting directly without listed_venue_name.
		_, err := testDB.Pool.Exec(ctx,
			"INSERT INTO events (id, venue_id, title, local_event_date, source_url) VALUES ($1, $2, $3, $4, $5)",
			"018b2f19-e591-7d12-bf9e-f0e74f1b4cc1", venue.ID, "Legacy Concert", concertDate, "https://example.com/legacy",
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

		artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4aa1", Name: "VenueName Test Band"}
		require.NoError(t, artistRepo.Create(ctx, artist))
		venue := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4bb1", Name: "VenueName Test Arena"}
		require.NoError(t, venueRepo.Create(ctx, venue))

		listedName := "Zepp Nagoya"
		err := concertRepo.Create(ctx, &entity.Concert{
			Event: entity.Event{
				ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4cc2",
				VenueID:         venue.ID,
				Title:           "Modern Concert",
				ListedVenueName: &listedName,
				LocalEventDate:  concertDate,
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
