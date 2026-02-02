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
	err := artistRepo.Create(ctx, testArtist)
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
		concert *entity.Concert
	}

	tests := []struct {
		name    string
		args    args
		wantErr error
	}{
		{
			name: "create valid concert",
			args: args{
				concert: &entity.Concert{
					ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c1",
					ArtistID:       "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
					Title:          "New Year's Eve Concert",
					LocalEventDate: concertDate,
					StartTime:      &startTime,
					OpenTime:       &openTime,
				},
			},
			wantErr: nil,
		},
		{
			name: "duplicate concert ID",
			args: args{
				concert: &entity.Concert{
					ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c1",
					ArtistID:       "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
					Title:          "Duplicate Concert",
					LocalEventDate: concertDate,
					StartTime:      &startTime,
					OpenTime:       &openTime,
				},
			},
			wantErr: apperr.ErrAlreadyExists,
		},
		{
			name: "foreign key violation - invalid artist",
			args: args{
				concert: &entity.Concert{
					ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c2",
					ArtistID:       "018b2f19-e591-7d12-bf9e-f0e74f1b49a0",
					VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
					Title:          "Invalid Artist Concert",
					LocalEventDate: concertDate,
					StartTime:      &startTime,
					OpenTime:       &openTime,
				},
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "foreign key violation - invalid venue",
			args: args{
				concert: &entity.Concert{
					ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c3",
					ArtistID:       "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
					VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b0",
					Title:          "Invalid Venue Concert",
					LocalEventDate: concertDate,
					StartTime:      &startTime,
					OpenTime:       &openTime,
				},
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := concertRepo.Create(ctx, tt.args.concert)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
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

	err := artistRepo.Create(ctx, testArtist1)
	require.NoError(t, err)
	err = artistRepo.Create(ctx, testArtist2)
	require.NoError(t, err)
	err = venueRepo.Create(ctx, testVenue)
	require.NoError(t, err)

	concertDate, _ := time.Parse("2006-01-02", "2026-06-15")
	startTime, _ := time.Parse("15:04", "20:00")
	openTime, _ := time.Parse("15:04", "18:00")
	startTime2, _ := time.Parse("15:04", "21:00")
	openTime2, _ := time.Parse("15:04", "19:00")

	// Create concerts
	concerts := []*entity.Concert{
		{
			ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c4",
			ArtistID:       "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
			VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
			Title:          "Concert 1",
			LocalEventDate: concertDate,
			StartTime:      &startTime,
			OpenTime:       &openTime,
		},
		{
			ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c5",
			ArtistID:       "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
			VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
			Title:          "Concert 2",
			LocalEventDate: concertDate.AddDate(0, 1, 0),
			StartTime:      &startTime2,
			OpenTime:       &openTime2,
		},
		{
			ID:             "018b2f19-e591-7d12-bf9e-f0e74f1b49c6",
			ArtistID:       "018b2f19-e591-7d12-bf9e-f0e74f1b49a3",
			VenueID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
			Title:          "Concert 3",
			LocalEventDate: concertDate,
			StartTime:      &startTime,
			OpenTime:       &openTime,
		},
	}

	for _, c := range concerts {
		err := concertRepo.Create(ctx, c)
		require.NoError(t, err)
	}

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
