package rdb_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr/codes"
)

func TestConcertRepository_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Setup: Create test artist and venue
	testArtist := &entity.Artist{
		ID:   "artist-concert-001",
		Name: "Concert Test Band",
	}
	if err := artistRepo.Create(ctx, testArtist); err != nil {
		t.Fatalf("Failed to setup test artist: %v", err)
	}

	testVenue := &entity.Venue{
		ID:   "venue-concert-001",
		Name: "Concert Test Arena",
	}
	if err := venueRepo.Create(ctx, testVenue); err != nil {
		t.Fatalf("Failed to setup test venue: %v", err)
	}

	concertDate, _ := time.Parse("2006-01-02", "2026-12-31")
	startTime, _ := time.Parse("15:04", "20:00")
	openTime, _ := time.Parse("15:04", "18:00")

	tests := []struct {
		name    string
		concert *entity.Concert
		wantErr bool
		errCode codes.Code
	}{
		{
			name: "create valid concert",
			concert: &entity.Concert{
				ID:        "concert-001",
				ArtistID:  "artist-concert-001",
				VenueID:   "venue-concert-001",
				Title:     "New Year's Eve Concert",
				Date:      concertDate,
				StartTime: startTime,
				OpenTime:  &openTime,
				Status:    entity.ConcertStatusScheduled,
			},
			wantErr: false,
		},
		{
			name: "duplicate concert ID",
			concert: &entity.Concert{
				ID:        "concert-001",
				ArtistID:  "artist-concert-001",
				VenueID:   "venue-concert-001",
				Title:     "Duplicate Concert",
				Date:      concertDate,
				StartTime: startTime,
				OpenTime:  &openTime,
				Status:    entity.ConcertStatusScheduled,
			},
			wantErr: true,
			errCode: codes.AlreadyExists,
		},
		{
			name: "foreign key violation - invalid artist",
			concert: &entity.Concert{
				ID:        "concert-002",
				ArtistID:  "artist-nonexistent",
				VenueID:   "venue-concert-001",
				Title:     "Invalid Artist Concert",
				Date:      concertDate,
				StartTime: startTime,
				OpenTime:  &openTime,
				Status:    entity.ConcertStatusScheduled,
			},
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
		{
			name: "foreign key violation - invalid venue",
			concert: &entity.Concert{
				ID:        "concert-003",
				ArtistID:  "artist-concert-001",
				VenueID:   "venue-nonexistent",
				Title:     "Invalid Venue Concert",
				Date:      concertDate,
				StartTime: startTime,
				OpenTime:  &openTime,
				Status:    entity.ConcertStatusScheduled,
			},
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := concertRepo.Create(ctx, tt.concert)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConcertRepository.Create() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				_, ok := err.(*connect.Error)
				if !ok {
					t.Errorf("Expected AppError, got %T", err)
					return
				}
				if connect.CodeOf(err) != tt.errCode.ToConnect() {
					t.Errorf("Expected error code %v, got %v", tt.errCode, connect.CodeOf(err))
				}
			}
		})
	}
}

func TestConcertRepository_ListByArtist(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Setup: Create test data
	testArtist1 := &entity.Artist{
		ID:   "artist-list-001",
		Name: "List Test Band 1",
	}
	testArtist2 := &entity.Artist{
		ID:   "artist-list-002",
		Name: "List Test Band 2",
	}
	testVenue := &entity.Venue{
		ID:   "venue-list-001",
		Name: "List Test Arena",
	}

	if err := artistRepo.Create(ctx, testArtist1); err != nil {
		t.Fatalf("Failed to setup test artist 1: %v", err)
	}
	if err := artistRepo.Create(ctx, testArtist2); err != nil {
		t.Fatalf("Failed to setup test artist 2: %v", err)
	}
	if err := venueRepo.Create(ctx, testVenue); err != nil {
		t.Fatalf("Failed to setup test venue: %v", err)
	}

	concertDate, _ := time.Parse("2006-01-02", "2026-06-15")
	startTime, _ := time.Parse("15:04", "20:00")
	openTime, _ := time.Parse("15:04", "18:00")
	startTime2, _ := time.Parse("15:04", "21:00")
	openTime2, _ := time.Parse("15:04", "19:00")

	// Create concerts
	concerts := []*entity.Concert{
		{
			ID:        "concert-list-001",
			ArtistID:  "artist-list-001",
			VenueID:   "venue-list-001",
			Title:     "Concert 1",
			Date:      concertDate,
			StartTime: startTime,
			OpenTime:  &openTime,
			Status:    entity.ConcertStatusScheduled,
		},
		{
			ID:        "concert-list-002",
			ArtistID:  "artist-list-001",
			VenueID:   "venue-list-001",
			Title:     "Concert 2",
			Date:      concertDate.AddDate(0, 1, 0),
			StartTime: startTime2,
			OpenTime:  &openTime2,
			Status:    entity.ConcertStatusScheduled,
		},
		{
			ID:        "concert-list-003",
			ArtistID:  "artist-list-002",
			VenueID:   "venue-list-001",
			Title:     "Concert 3",
			Date:      concertDate,
			StartTime: startTime,
			OpenTime:  &openTime,
			Status:    entity.ConcertStatusScheduled,
		},
	}

	for _, c := range concerts {
		if err := concertRepo.Create(ctx, c); err != nil {
			t.Fatalf("Failed to setup test concert: %v", err)
		}
	}

	tests := []struct {
		name          string
		artistID      string
		wantErr       bool
		expectedCount int
		validate      func(t *testing.T, concerts []*entity.Concert)
	}{
		{
			name:          "list concerts for artist with 2 concerts",
			artistID:      "artist-list-001",
			wantErr:       false,
			expectedCount: 2,
			validate: func(t *testing.T, concerts []*entity.Concert) {
				if len(concerts) != 2 {
					t.Errorf("Expected 2 concerts, got %d", len(concerts))
					return
				}
				// Verify all concerts belong to the correct artist
				for _, c := range concerts {
					if c.ArtistID != "artist-list-001" {
						t.Errorf("Expected ArtistID 'artist-list-001', got '%s'", c.ArtistID)
					}
					if c.CreatedAt.IsZero() {
						t.Error("Expected CreatedAt to be set")
					}
				}
			},
		},
		{
			name:          "list concerts for artist with 1 concert",
			artistID:      "artist-list-002",
			wantErr:       false,
			expectedCount: 1,
			validate: func(t *testing.T, concerts []*entity.Concert) {
				if len(concerts) != 1 {
					t.Errorf("Expected 1 concert, got %d", len(concerts))
					return
				}
				if concerts[0].Title != "Concert 3" {
					t.Errorf("Expected title 'Concert 3', got '%s'", concerts[0].Title)
				}
			},
		},
		{
			name:          "list concerts for artist with no concerts",
			artistID:      "artist-nonexistent",
			wantErr:       false,
			expectedCount: 0,
			validate: func(t *testing.T, concerts []*entity.Concert) {
				if len(concerts) != 0 {
					t.Errorf("Expected 0 concerts, got %d", len(concerts))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := concertRepo.ListByArtist(ctx, tt.artistID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConcertRepository.ListByArtist() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.validate != nil {
				tt.validate(t, got)
			}
		})
	}
}
