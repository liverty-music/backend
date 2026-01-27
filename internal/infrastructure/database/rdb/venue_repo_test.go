package rdb_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr/codes"
)

func TestVenueRepository_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

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
				ID:   "venue-001",
				Name: "Test Arena",
			},
			wantErr: false,
		},
		{
			name: "duplicate venue ID",
			venue: &entity.Venue{
				ID:   "venue-001",
				Name: "Duplicate Arena",
			},
			wantErr: true,
			errCode: codes.AlreadyExists,
		},
		{
			name: "empty venue name",
			venue: &entity.Venue{
				ID:   "venue-002",
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

func TestVenueRepository_Get(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Setup: Create a test venue
	testVenue := &entity.Venue{
		ID:   "venue-get-001",
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
			venueID: "venue-get-001",
			wantErr: false,
			validate: func(t *testing.T, venue *entity.Venue) {
				if venue == nil {
					t.Fatal("Expected venue, got nil")
				}
				if venue.ID != "venue-get-001" {
					t.Errorf("Expected ID 'venue-get-001', got '%s'", venue.ID)
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
			venueID: "venue-nonexistent",
			wantErr: true,
			errCode: codes.NotFound,
		},
		{
			name:    "get with empty ID",
			venueID: "",
			wantErr: true,
			errCode: codes.NotFound,
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
				_, ok := err.(*connect.Error)
				if !ok {
					t.Errorf("Expected AppError, got %T", err)
					return
				}
				if connect.CodeOf(err) != tt.errCode.ToConnect() {
					t.Errorf("Expected error code %v, got %v", tt.errCode, connect.CodeOf(err))
				}
			}

			if !tt.wantErr && tt.validate != nil {
				tt.validate(t, got)
			}
		})
	}
}
