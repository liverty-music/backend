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
					AdminArea: strPtr("愛知県"),
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

	// Setup: Create test venues
	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
		Name: "Get Test Arena",
	}
	err := repo.Create(ctx, testVenue)
	require.NoError(t, err)

	testVenueWithAdminArea := &entity.Venue{
		ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
		Name:      "Zepp Tokyo",
		AdminArea: strPtr("東京都"),
	}
	err = repo.Create(ctx, testVenueWithAdminArea)
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
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
				Name:      "Get Test Arena",
				AdminArea: nil,
			},
			wantErr: nil,
		},
		{
			name: "get venue with admin_area",
			args: struct {
				venueID string
			}{
				venueID: "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
			},
			want: &entity.Venue{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
				Name:      "Zepp Tokyo",
				AdminArea: strPtr("東京都"),
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
			if tt.want.AdminArea == nil {
				assert.Nil(t, got.AdminArea)
			} else {
				require.NotNil(t, got.AdminArea)
				assert.Equal(t, *tt.want.AdminArea, *got.AdminArea)
			}
		})
	}
}

func TestVenueRepository_GetByName(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Setup: Create test venues
	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e7",
		Name: "GetByName Test Arena",
	}
	err := repo.Create(ctx, testVenue)
	require.NoError(t, err)

	testVenueWithAdminArea := &entity.Venue{
		ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e8",
		Name:      "Zepp Osaka Bayside",
		AdminArea: strPtr("大阪府"),
	}
	err = repo.Create(ctx, testVenueWithAdminArea)
	require.NoError(t, err)

	tests := []struct {
		name string
		args struct {
			venueName string
		}
		want    *entity.Venue
		wantErr error
	}{
		{
			name: "get existing venue by name",
			args: struct {
				venueName string
			}{
				venueName: "GetByName Test Arena",
			},
			want: &entity.Venue{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e7",
				Name:      "GetByName Test Arena",
				AdminArea: nil,
			},
			wantErr: nil,
		},
		{
			name: "get venue with admin_area by name",
			args: struct {
				venueName string
			}{
				venueName: "Zepp Osaka Bayside",
			},
			want: &entity.Venue{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e8",
				Name:      "Zepp Osaka Bayside",
				AdminArea: strPtr("大阪府"),
			},
			wantErr: nil,
		},
		{
			name: "get non-existent venue by name",
			args: struct {
				venueName string
			}{
				venueName: "Does Not Exist",
			},
			want:    nil,
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.GetByName(ctx, tt.args.venueName)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
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
