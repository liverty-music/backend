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

func strPtr(s string) *string { return &s }

func TestVenueRepository_Create(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name string
		args struct {
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
			name: "create venue with admin_area",
			args: struct {
				venue *entity.Venue
			}{
				venue: &entity.Venue{
					ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e5",
					Name:      "Zepp Nagoya",
					AdminArea: strPtr("JP-23"),
				},
			},
			wantErr: nil,
		},
		{
			name: "create venue with google_place_id and coordinates",
			args: struct {
				venue *entity.Venue
			}{
				venue: &entity.Venue{
					ID:            "018b2f19-e591-7d12-bf9e-f0e74f1b49ea",
					Name:          "Zepp Sapporo",
					GooglePlaceID: strPtr("ChIJtest123"),
					Coordinates:   &entity.Coordinates{Latitude: 43.0618, Longitude: 141.3545},
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
			wantErr: apperr.ErrInvalidArgument,
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

	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
		Name: "Get Test Arena",
	}
	require.NoError(t, repo.Create(ctx, testVenue))

	testVenueWithAdminArea := &entity.Venue{
		ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
		Name:      "Zepp Tokyo",
		AdminArea: strPtr("JP-13"),
	}
	require.NoError(t, repo.Create(ctx, testVenueWithAdminArea))

	tests := []struct {
		name    string
		id      string
		want    *entity.Venue
		wantErr error
	}{
		{
			name: "get existing venue",
			id:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
			want: &entity.Venue{
				ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
				Name: "Get Test Arena",
			},
		},
		{
			name: "get venue with admin_area",
			id:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
			want: &entity.Venue{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
				Name:      "Zepp Tokyo",
				AdminArea: strPtr("JP-13"),
			},
		},
		{
			name:    "get non-existent venue",
			id:      "018b2f19-e591-7d12-bf9e-f0e74f1b49e0",
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "get with empty ID",
			id:      "",
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.Get(ctx, tt.id)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.want.ID, got.ID)
			assert.Equal(t, tt.want.Name, got.Name)
			if tt.want.AdminArea == nil {
				assert.Nil(t, got.AdminArea)
			} else {
				require.NotNil(t, got.AdminArea)
				assert.Equal(t, *tt.want.AdminArea, *got.AdminArea)
			}
		})
	}
}
