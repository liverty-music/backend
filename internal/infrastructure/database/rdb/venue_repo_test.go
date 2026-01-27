package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

func TestVenueRepository_Create(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		venue   *entity.Venue
		wantErr bool
		errCode codes.Code
	}{
		{
			name: "create valid venue",
			venue: &entity.Venue{
				ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e1",
				Name: "Test Arena",
			},
			wantErr: false,
		},
		{
			name: "duplicate venue ID",
			venue: &entity.Venue{
				ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e1",
				Name: "Duplicate Arena",
			},
			wantErr: true,
			errCode: codes.AlreadyExists,
		},
		{
			name: "empty venue name",
			venue: &entity.Venue{
				ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e2",
				Name: "",
			},
			wantErr: false, // Database allows empty strings
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.Create(ctx, tt.venue)
			if (err != nil) != tt.wantErr {
				t.Errorf("VenueRepository.Create() error = %v, wantErr %v", err, tt.wantErr)
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

func TestVenueRepository_Get(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Setup: Create a test venue
	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
		Name: "Get Test Arena",
	}
	if err := repo.Create(ctx, testVenue); err != nil {
		t.Fatalf("Failed to setup test venue: %v", err)
	}

	tests := []struct {
		name     string
		venueID  string
		wantErr  bool
		errCode  codes.Code
		validate func(t *testing.T, venue *entity.Venue)
	}{
		{
			name:    "get existing venue",
			venueID: "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
			wantErr: false,
			validate: func(t *testing.T, venue *entity.Venue) {
				if venue == nil {
					t.Fatal("Expected venue, got nil")
				}
				if venue.ID != "018b2f19-e591-7d12-bf9e-f0e74f1b49e3" {
					t.Errorf("Expected ID '018b2f19-e591-7d12-bf9e-f0e74f1b49e3', got '%s'", venue.ID)
				}
				if venue.Name != "Get Test Arena" {
					t.Errorf("Expected name 'Get Test Arena', got '%s'", venue.Name)
				}
				if venue.CreatedAt.IsZero() {
					t.Error("Expected CreatedAt to be set")
				}
				if venue.UpdatedAt.IsZero() {
					t.Error("Expected UpdatedAt to be set")
				}
			},
		},
		{
			name:    "get non-existent venue",
			venueID: "018b2f19-e591-7d12-bf9e-f0e74f1b49e0",
			wantErr: true,
			errCode: codes.NotFound,
		},
		{
			name:    "get with empty ID",
			venueID: "",
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.Get(ctx, tt.venueID)
			if (err != nil) != tt.wantErr {
				t.Errorf("VenueRepository.Get() error = %v, wantErr %v", err, tt.wantErr)
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

			if !tt.wantErr && tt.validate != nil {
				tt.validate(t, got)
			}
		})
	}
}
