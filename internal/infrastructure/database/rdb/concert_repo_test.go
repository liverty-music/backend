package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

func TestConcertRepository_Create(t *testing.T) {
	cleanDatabase()
	concertRepo := rdb.NewConcertRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	venueRepo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Setup: Create test artist and venue
	testArtist := &entity.Artist{
		ID:            "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
		Name:          "Concert Test Band",
		SpotifyID:     "spotify-create-001",
		MusicBrainzID: "mb-create-001",
	}
	if err := artistRepo.Create(ctx, testArtist); err != nil {
		t.Fatalf("Failed to setup test artist: %v", err)
	}

	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
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
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c1",
				ArtistID:  "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
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
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c1",
				ArtistID:  "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
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
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c2",
				ArtistID:  "018b2f19-e591-7d12-bf9e-f0e74f1b49a0",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b1",
				Title:     "Invalid Artist Concert",
				Date:      concertDate,
				StartTime: startTime,
				OpenTime:  &openTime,
				Status:    entity.ConcertStatusScheduled,
			},
			wantErr: true,
			errCode: codes.FailedPrecondition,
		},
		{
			name: "foreign key violation - invalid venue",
			concert: &entity.Concert{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c3",
				ArtistID:  "018b2f19-e591-7d12-bf9e-f0e74f1b49a1",
				VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b0",
				Title:     "Invalid Venue Concert",
				Date:      concertDate,
				StartTime: startTime,
				OpenTime:  &openTime,
				Status:    entity.ConcertStatusScheduled,
			},
			wantErr: true,
			errCode: codes.FailedPrecondition,
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
				appErr, ok := err.(*apperr.AppErr)
				if !ok {
					t.Errorf("Expected *apperr.AppErr, got %T", err)
					return
				}
				if appErr.Code != tt.errCode {
					t.Errorf("Expected error code %v, got %v", tt.errCode, appErr.Code)
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
		ID:            "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
		Name:          "List Test Band 1",
		SpotifyID:     "spotify-list-001",
		MusicBrainzID: "mb-list-001",
	}
	testArtist2 := &entity.Artist{
		ID:            "018b2f19-e591-7d12-bf9e-f0e74f1b49a3",
		Name:          "List Test Band 2",
		SpotifyID:     "spotify-list-002",
		MusicBrainzID: "mb-list-002",
	}
	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
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
			ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c4",
			ArtistID:  "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
			VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
			Title:     "Concert 1",
			Date:      concertDate,
			StartTime: startTime,
			OpenTime:  &openTime,
			Status:    entity.ConcertStatusScheduled,
		},
		{
			ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c5",
			ArtistID:  "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
			VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
			Title:     "Concert 2",
			Date:      concertDate.AddDate(0, 1, 0),
			StartTime: startTime2,
			OpenTime:  &openTime2,
			Status:    entity.ConcertStatusScheduled,
		},
		{
			ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49c6",
			ArtistID:  "018b2f19-e591-7d12-bf9e-f0e74f1b49a3",
			VenueID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49b2",
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
			artistID:      "018b2f19-e591-7d12-bf9e-f0e74f1b49a2",
			wantErr:       false,
			expectedCount: 2,
			validate: func(t *testing.T, concerts []*entity.Concert) {
				if len(concerts) != 2 {
					t.Errorf("Expected 2 concerts, got %d", len(concerts))
					return
				}
				// Verify all concerts belong to the correct artist
				for _, c := range concerts {
					if c.ArtistID != "018b2f19-e591-7d12-bf9e-f0e74f1b49a2" {
						t.Errorf("Expected ArtistID '018b2f19-e591-7d12-bf9e-f0e74f1b49a2', got '%s'", c.ArtistID)
					}
					if c.CreatedAt.IsZero() {
						t.Error("Expected CreatedAt to be set")
					}
				}
			},
		},
		{
			name:          "list concerts for artist with 1 concert",
			artistID:      "018b2f19-e591-7d12-bf9e-f0e74f1b49a3",
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
			artistID:      "018b2f19-e591-7d12-bf9e-f0e74f1b49a0",
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
