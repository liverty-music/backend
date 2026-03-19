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

	type args struct {
		venue *entity.Venue
	}

	tests := []struct {
		name    string
		args    args
		wantErr error
	}{
		{
			name: "create valid venue",
			args: args{
				venue: &entity.Venue{
					ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e1",
					Name: "Test Arena",
				},
			},
			wantErr: nil,
		},
		{
			name: "create venue with admin_area",
			args: args{
				venue: &entity.Venue{
					ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e5",
					Name:      "Zepp Nagoya",
					AdminArea: ptr("JP-23"),
				},
			},
			wantErr: nil,
		},
		{
			name: "create venue with google_place_id and coordinates",
			args: args{
				venue: &entity.Venue{
					ID:            "018b2f19-e591-7d12-bf9e-f0e74f1b49ea",
					Name:          "Zepp Sapporo",
					GooglePlaceID: ptr("ChIJtest123"),
					Coordinates:   &entity.Coordinates{Latitude: 43.0618, Longitude: 141.3545},
				},
			},
			wantErr: nil,
		},
		{
			name: "duplicate venue ID",
			args: args{
				venue: &entity.Venue{
					ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e1",
					Name: "Duplicate Arena",
				},
			},
			wantErr: apperr.ErrAlreadyExists,
		},
		{
			name: "empty venue name",
			args: args{
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
		AdminArea: ptr("JP-13"),
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
				AdminArea: ptr("JP-13"),
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

func TestVenueRepository_GetByPlaceID(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Seed a venue with a known GooglePlaceID so the happy-path test can find it.
	seededVenue := &entity.Venue{
		ID:            "018b2f19-e591-7d12-bf9e-f0e74f1b49eb",
		Name:          "Place ID Test Arena",
		GooglePlaceID: ptr("ChIJtest456"),
	}
	require.NoError(t, repo.Create(ctx, seededVenue))

	type args struct {
		placeID string
	}

	tests := []struct {
		name    string
		args    args
		want    *entity.Venue
		wantErr error
	}{
		{
			name:    "returns venue by place ID",
			args:    args{placeID: "ChIJtest456"},
			want:    seededVenue,
			wantErr: nil,
		},
		{
			name:    "returns NotFound for unknown place ID",
			args:    args{placeID: "ChIJunknown999"},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.GetByPlaceID(ctx, tt.args.placeID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.want.ID, got.ID)
			assert.Equal(t, tt.want.Name, got.Name)
			require.NotNil(t, got.GooglePlaceID)
			assert.Equal(t, *tt.want.GooglePlaceID, *got.GooglePlaceID)
		})
	}
}
