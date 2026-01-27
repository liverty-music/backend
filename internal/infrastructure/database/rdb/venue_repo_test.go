package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVenueRepository_Create(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		args    struct {
			venue *entity.Venue
		}
		wantErr error
	}{
		{
			name: "create valid venue",
			args: struct {
				venue *entity.Venue
			}{
				venue: &entity.Venue{
					ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e1",
					Name: "Test Arena",
				},
			},
			wantErr: nil,
		},
		{
			name: "duplicate venue ID",
			args: struct {
				venue *entity.Venue
			}{
				venue: &entity.Venue{
					ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e1",
					Name: "Duplicate Arena",
				},
			},
			wantErr: apperr.ErrAlreadyExists,
		},
		{
			name: "empty venue name",
			args: struct {
				venue *entity.Venue
			}{
				venue: &entity.Venue{
					ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e2",
					Name: "",
				},
			},
			wantErr: nil, // Database allows empty strings
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.Create(ctx, tt.args.venue)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
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
	err := repo.Create(ctx, testVenue)
	require.NoError(t, err)

	tests := []struct {
		name string
		args struct {
			venueID string
		}
		want    *entity.Venue
		wantErr error
	}{
		{
			name: "get existing venue",
			args: struct {
				venueID string
			}{
				venueID: "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
			},
			want: &entity.Venue{
				ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
				Name: "Get Test Arena",
			},
			wantErr: nil,
		},
		{
			name: "get non-existent venue",
			args: struct {
				venueID string
			}{
				venueID: "018b2f19-e591-7d12-bf9e-f0e74f1b49e0",
			},
			want:    nil,
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "get with empty ID",
			args: struct {
				venueID string
			}{
				venueID: "",
			},
			want:    nil,
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.Get(ctx, tt.args.venueID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.want.ID, got.ID)
			assert.Equal(t, tt.want.Name, got.Name)
			assert.False(t, got.CreatedAt.IsZero())
			assert.False(t, got.UpdatedAt.IsZero())
		})
	}
}
