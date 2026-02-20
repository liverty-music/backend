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
			name: "NULL listed_venue_name (pre-migration row) is scanned to nil without error",
			setup: func(t *testing.T, artistID, venueID string) {
				t.Helper()
				// Simulate a pre-migration row by inserting directly without listed_venue_name.
				// This exercises the NULL → *string nil mapping that was broken before this fix.
				_, err := testDB.Pool.Exec(ctx,
					"INSERT INTO events (id, venue_id, title, local_event_date, source_url) VALUES ($1, $2, $3, $4, $5)",
					"018b2f19-e591-7d12-bf9e-f0e74f1b4cc1", venueID, "Legacy Concert", concertDate, "https://example.com/legacy",
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
						LocalEventDate:  concertDate,
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

			artist := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4aa1", Name: "VenueName Test Band"}
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

	concertDate, _ := time.Parse("2006-01-02", "2026-06-15")
	startTime, _ := time.Parse("15:04", "20:00")
	openTime, _ := time.Parse("15:04", "18:00")
	startTime2, _ := time.Parse("15:04", "21:00")
	openTime2, _ := time.Parse("15:04", "19:00")

	// Create concerts using bulk insert
	concerts := []*entity.Concert{
		{
			Event: entity.Event{
				ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c4",
				VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:          "Concert 1",
				LocalEventDate: concertDate,
				StartTime:      &startTime,
				OpenTime:       &openTime,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		},
		{
			Event: entity.Event{
				ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c5",
				VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:          "Concert 2",
				LocalEventDate: concertDate.AddDate(0, 1, 0),
				StartTime:      &startTime2,
				OpenTime:       &openTime2,
			},
			ArtistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		},
		{
			Event: entity.Event{
				ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c6",
				VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
				Title:          "Concert 3",
				LocalEventDate: concertDate,
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
			name: "list concerts for artist with 2 concerts",
			args: args{
				artistID: "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
			},
			want: struct {
				count int
			}{
				count: 2,
			},
			wantErr: nil,
			validate: func(t *testing.T, concerts []*entity.Concert) {
				for _, c := range concerts {
					assert.Equal(t, "018b2f19-e591-7d12-bf9e-f0e74f1b49a2", c.ArtistID)
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
}
