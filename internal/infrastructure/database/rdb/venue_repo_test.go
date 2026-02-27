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
			name: "create venue with explicit enrichment fields",
			args: struct {
				venue *entity.Venue
			}{
				venue: &entity.Venue{
					ID:               "018b2f19-e591-7d12-bf9e-f0e74f1b49ea",
					Name:             "Zepp Sapporo",
					EnrichmentStatus: entity.EnrichmentStatusPending,
					RawName:          "Zepp Sapporo",
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
				ID:               "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
				Name:             "Get Test Arena",
				AdminArea:        nil,
				EnrichmentStatus: entity.EnrichmentStatusPending,
				RawName:          "Get Test Arena",
			},
		},
		{
			name: "get venue with admin_area",
			id:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
			want: &entity.Venue{
				ID:               "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
				Name:             "Zepp Tokyo",
				AdminArea:        strPtr("JP-13"),
				EnrichmentStatus: entity.EnrichmentStatusPending,
				RawName:          "Zepp Tokyo",
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
			assert.Equal(t, tt.want.EnrichmentStatus, got.EnrichmentStatus)
			assert.Equal(t, tt.want.RawName, got.RawName)
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

	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e7",
		Name: "GetByName Test Arena",
	}
	require.NoError(t, repo.Create(ctx, testVenue))

	testVenueWithAdminArea := &entity.Venue{
		ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e8",
		Name:      "Zepp Osaka Bayside",
		AdminArea: strPtr("JP-27"),
	}
	require.NoError(t, repo.Create(ctx, testVenueWithAdminArea))

	tests := []struct {
		name      string
		queryName string
		wantID    string
		wantErr   error
	}{
		{
			name:      "get existing venue by name",
			queryName: "GetByName Test Arena",
			wantID:    "018b2f19-e591-7d12-bf9e-f0e74f1b49e7",
		},
		{
			name:      "get venue with admin_area by name",
			queryName: "Zepp Osaka Bayside",
			wantID:    "018b2f19-e591-7d12-bf9e-f0e74f1b49e8",
		},
		{
			name:      "get non-existent venue by name",
			queryName: "Does Not Exist",
			wantErr:   apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.GetByName(ctx, tt.queryName)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantID, got.ID)
		})
	}
}

func TestVenueRepository_GetByName_RawNameFallback(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Simulate an enriched venue: raw_name = original scraper name, name = canonical
	original := &entity.Venue{
		ID:      "018b2f19-e591-7d12-bf9e-f0e74f1b49eb",
		Name:    "NIPPON BUDOKAN",
		RawName: "日本武道館",
	}
	require.NoError(t, repo.Create(ctx, original))

	t.Run("lookup by canonical name", func(t *testing.T) {
		got, err := repo.GetByName(ctx, "NIPPON BUDOKAN")
		require.NoError(t, err)
		assert.Equal(t, original.ID, got.ID)
	})

	t.Run("lookup by raw name falls back correctly", func(t *testing.T) {
		got, err := repo.GetByName(ctx, "日本武道館")
		require.NoError(t, err)
		assert.Equal(t, original.ID, got.ID)
	})
}

func TestVenueRepository_ListPending(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	pending1 := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4901", Name: "Pending Venue 1"}
	pending2 := &entity.Venue{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4902", Name: "Pending Venue 2"}
	require.NoError(t, repo.Create(ctx, pending1))
	require.NoError(t, repo.Create(ctx, pending2))

	// Mark one as failed so it won't appear in ListPending
	require.NoError(t, repo.MarkFailed(ctx, pending2.ID))

	got, err := repo.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, pending1.ID, got[0].ID)
	assert.Equal(t, entity.EnrichmentStatusPending, got[0].EnrichmentStatus)
}

func TestVenueRepository_UpdateEnriched(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	v := &entity.Venue{
		ID:      "018b2f19-e591-7d12-bf9e-f0e74f1b4903",
		Name:    "zepp nagoya",
		RawName: "zepp nagoya",
	}
	require.NoError(t, repo.Create(ctx, v))

	mbid := "a2e6e2c0-1234-5678-abcd-000000000001"
	enriched := &entity.Venue{
		ID:      v.ID,
		Name:    "Zepp Nagoya",
		RawName: "zepp nagoya",
		MBID:    &mbid,
	}
	require.NoError(t, repo.UpdateEnriched(ctx, enriched))

	got, err := repo.Get(ctx, v.ID)
	require.NoError(t, err)
	assert.Equal(t, "Zepp Nagoya", got.Name)
	assert.Equal(t, "zepp nagoya", got.RawName)
	require.NotNil(t, got.MBID)
	assert.Equal(t, mbid, *got.MBID)
	assert.Equal(t, entity.EnrichmentStatusEnriched, got.EnrichmentStatus)
}

func TestVenueRepository_MarkFailed(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	v := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b4904",
		Name: "Unknown Venue",
	}
	require.NoError(t, repo.Create(ctx, v))
	require.NoError(t, repo.MarkFailed(ctx, v.ID))

	got, err := repo.Get(ctx, v.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.EnrichmentStatusFailed, got.EnrichmentStatus)
}

func TestVenueRepository_MergeVenues(t *testing.T) {
	cleanDatabase()
	venueRepo := rdb.NewVenueRepository(testDB)
	concertRepo := rdb.NewConcertRepository(testDB)
	ctx := context.Background()

	// Create an artist
	artistID := "018b2f19-e591-7d12-bf9e-000000000001"
	_, err := testDB.Pool.Exec(ctx, `INSERT INTO artists (id, name) VALUES ($1, $2)`, artistID, "Test Artist")
	require.NoError(t, err)

	// Canonical venue (older)
	canonical := &entity.Venue{
		ID:      "018b2f19-e591-7d12-bf9e-f0e74f1b4910",
		Name:    "Zepp Nagoya",
		RawName: "Zepp Nagoya",
	}
	require.NoError(t, venueRepo.Create(ctx, canonical))

	// Duplicate venue
	duplicate := &entity.Venue{
		ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b4911",
		Name:      "zepp nagoya",
		RawName:   "zepp nagoya",
		AdminArea: strPtr("JP-23"),
	}
	require.NoError(t, venueRepo.Create(ctx, duplicate))

	// Create a concert on the duplicate that shares (artist, date, start_at) with a canonical concert
	// → should be deleted
	dupEvent1ID, dupConcert1ID := "018b2f19-0000-0000-0000-000000000011", "018b2f19-0000-0000-0001-000000000011"
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, venue_id, title, local_event_date) VALUES ($1, $2, 'Show', '2026-03-01')`,
		dupEvent1ID, duplicate.ID)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO concerts (event_id, artist_id) VALUES ($1, $2)`,
		dupEvent1ID, artistID)
	require.NoError(t, err)
	_ = dupConcert1ID

	// Also create the same concert on canonical → dupEvent1 should be deleted
	canEvent1ID := "018b2f19-0000-0000-0000-000000000010"
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, venue_id, title, local_event_date) VALUES ($1, $2, 'Show', '2026-03-01')`,
		canEvent1ID, canonical.ID)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO concerts (event_id, artist_id) VALUES ($1, $2)`,
		canEvent1ID, artistID)
	require.NoError(t, err)

	// A unique concert only on duplicate → should be re-pointed to canonical
	dupEvent2ID := "018b2f19-0000-0000-0000-000000000012"
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, venue_id, title, local_event_date) VALUES ($1, $2, 'Unique Show', '2026-04-01')`,
		dupEvent2ID, duplicate.ID)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO concerts (event_id, artist_id) VALUES ($1, $2)`,
		dupEvent2ID, artistID)
	require.NoError(t, err)

	_ = concertRepo

	require.NoError(t, venueRepo.MergeVenues(ctx, canonical.ID, duplicate.ID))

	// Duplicate venue should be gone
	_, err = venueRepo.Get(ctx, duplicate.ID)
	assert.ErrorIs(t, err, apperr.ErrNotFound)

	// Canonical venue should have admin_area COALESCEd from duplicate
	got, err := venueRepo.Get(ctx, canonical.ID)
	require.NoError(t, err)
	require.NotNil(t, got.AdminArea)
	assert.Equal(t, "JP-23", *got.AdminArea)

	// Duplicate-only event should now belong to canonical
	var venueID string
	err = testDB.Pool.QueryRow(ctx, `SELECT venue_id FROM events WHERE id = $1`, dupEvent2ID).Scan(&venueID)
	require.NoError(t, err)
	assert.Equal(t, canonical.ID, venueID)

	// The duplicate event that shared (artist, date, start_at) with canonical should be deleted
	var count int
	err = testDB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM events WHERE id = $1`, dupEvent1ID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
