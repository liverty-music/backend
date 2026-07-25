package rdb_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVenueRepository_Create(t *testing.T) {
	cleanDatabase(t)
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
					AdminArea: new("JP-23"),
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
					GooglePlaceID: new("ChIJtest123"),
					Coordinates:   &entity.Coordinates{Latitude: 43.0618, Longitude: 141.3545},
				},
			},
			wantErr: nil,
		},
		{
			// Untargeted ON CONFLICT DO NOTHING also absorbs a primary-key
			// collision; with no place_id or listed_venue_name to re-SELECT on,
			// there is no surviving natural-key row to return, so Create surfaces
			// Internal rather than silently returning an empty id. (A real UUIDv7
			// never collides, so this only guards the degenerate path.)
			name: "duplicate venue ID with no natural key",
			args: args{
				venue: &entity.Venue{
					ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e1",
					Name: "Duplicate Arena",
				},
			},
			wantErr: apperr.ErrInternal,
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
			_, err := repo.Create(ctx, tt.args.venue)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestVenueRepository_Get(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	testVenue := &entity.Venue{
		ID:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
		Name: "Get Test Arena",
	}
	{
		_, cErr := repo.Create(ctx, testVenue)
		require.NoError(t, cErr)
	}

	testVenueWithAdminArea := &entity.Venue{
		ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
		Name:      "Zepp Tokyo",
		AdminArea: new("JP-13"),
	}
	{
		_, cErr := repo.Create(ctx, testVenueWithAdminArea)
		require.NoError(t, cErr)
	}

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
				AdminArea: new("JP-13"),
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

func TestVenueRepository_GetByListedName(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	adminArea := "JP-13"
	listedName := "武道館"

	// Seed: venue with listed_venue_name and admin_area.
	seededWithArea := &entity.Venue{
		ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b50a1",
		Name:            "Nippon Budokan",
		AdminArea:       &adminArea,
		GooglePlaceID:   new("ChIJbudokan001"),
		ListedVenueName: &listedName,
	}
	{
		_, cErr := repo.Create(ctx, seededWithArea)
		require.NoError(t, cErr)
	}

	// Seed: venue with listed_venue_name and NULL admin_area.
	listedNameNoArea := "Zepp DiverCity"
	seededNoArea := &entity.Venue{
		ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b50a2",
		Name:            "Zepp DiverCity Tokyo",
		GooglePlaceID:   new("ChIJzepp001"),
		ListedVenueName: &listedNameNoArea,
	}
	{
		_, cErr := repo.Create(ctx, seededNoArea)
		require.NoError(t, cErr)
	}

	type args struct {
		listedVenueName string
		adminArea       *string
	}
	tests := []struct {
		name    string
		args    args
		wantID  string
		wantErr error
	}{
		{
			name:   "found by listed name and admin area",
			args:   args{listedVenueName: "武道館", adminArea: &adminArea},
			wantID: seededWithArea.ID,
		},
		{
			name:   "found by listed name with NULL admin area",
			args:   args{listedVenueName: "Zepp DiverCity", adminArea: nil},
			wantID: seededNoArea.ID,
		},
		{
			name:    "not found: unknown listed name",
			args:    args{listedVenueName: "Unknown Hall", adminArea: nil},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "not found: correct name but wrong admin area",
			args:    args{listedVenueName: "武道館", adminArea: new("JP-27")},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.GetByListedName(ctx, tt.args.listedVenueName, tt.args.adminArea)
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

func TestVenueRepository_GetByPlaceID(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// Seed a venue with a known GooglePlaceID so the happy-path test can find it.
	seededVenue := &entity.Venue{
		ID:            "018b2f19-e591-7d12-bf9e-f0e74f1b49eb",
		Name:          "Place ID Test Arena",
		GooglePlaceID: new("ChIJtest456"),
	}
	{
		_, cErr := repo.Create(ctx, seededVenue)
		require.NoError(t, cErr)
	}

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

// countVenuesByListedName returns how many venue rows carry the given
// listed_venue_name — used to assert get-or-create never splits a venue.
func countVenuesByListedName(t *testing.T, ctx context.Context, listedName string) int {
	t.Helper()
	var n int
	err := testDB.Pool.QueryRow(ctx,
		"SELECT count(*) FROM venues WHERE listed_venue_name = $1", listedName).Scan(&n)
	require.NoError(t, err)
	return n
}

func TestVenueRepository_Create_GetOrCreate(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	// A physical venue that Google Places resolved with two different CIDs across
	// discovery runs (the observed 和歌山ビッグホエール case).
	const listedName = "和歌山ビッグホエール"
	existing := &entity.Venue{
		ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4a01",
		Name:            "Wakayama Big Whale",
		AdminArea:       new("JP-30"),
		GooglePlaceID:   new("CID-A"),
		ListedVenueName: new(listedName),
	}
	firstID, err := repo.Create(ctx, existing)
	require.NoError(t, err)
	assert.Equal(t, existing.ID, firstID)

	t.Run("different place_id resolves to the existing row", func(t *testing.T) {
		// Same (listed_venue_name, admin_area) but a DIFFERENT place_id — the
		// insert conflicts on idx_venues_listed_name_admin_area and re-SELECTs.
		gotID, err := repo.Create(ctx, &entity.Venue{
			ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4a02",
			Name:            "Wakayama Big Whale (dup)",
			AdminArea:       new("JP-30"),
			GooglePlaceID:   new("CID-B"),
			ListedVenueName: new(listedName),
		})
		require.NoError(t, err)
		assert.Equal(t, existing.ID, gotID)
		assert.Equal(t, 1, countVenuesByListedName(t, ctx, listedName))
	})
}

func TestVenueRepository_Create_ConcurrentSingleRow(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	const listedName = "Concurrent Hall"
	const n = 8
	ids := make([]string, n)
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct primary keys, identical (listed_venue_name, admin_area),
			// no place_id: exactly one insert wins; the rest re-SELECT the winner.
			id, err := repo.Create(ctx, &entity.Venue{
				ID:              uuid.Must(uuid.NewV7()).String(),
				Name:            "Concurrent Hall Canonical",
				AdminArea:       new("JP-13"),
				ListedVenueName: new(listedName),
			})
			ids[i] = id
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i := range n {
		require.NoError(t, errs[i])
		assert.Equal(t, ids[0], ids[i], "every concurrent create must resolve to the same surviving row")
	}
	assert.Equal(t, 1, countVenuesByListedName(t, ctx, listedName))
}

func TestVenueRepository_BackfillPlaceID(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewVenueRepository(testDB)
	ctx := context.Background()

	t.Run("fills a NULL place_id and never overwrites a non-NULL one", func(t *testing.T) {
		v := &entity.Venue{
			ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4b01",
			Name:            "Backfill Hall",
			ListedVenueName: new("Backfill Hall Listed"),
		}
		_, err := repo.Create(ctx, v)
		require.NoError(t, err)

		require.NoError(t, repo.BackfillPlaceID(ctx, v.ID, "CID-FILL"))
		got, err := repo.Get(ctx, v.ID)
		require.NoError(t, err)
		require.NotNil(t, got.GooglePlaceID)
		assert.Equal(t, "CID-FILL", *got.GooglePlaceID)

		// Second backfill is a no-op: the WHERE guard skips a non-NULL row.
		require.NoError(t, repo.BackfillPlaceID(ctx, v.ID, "CID-OTHER"))
		got, err = repo.Get(ctx, v.ID)
		require.NoError(t, err)
		require.NotNil(t, got.GooglePlaceID)
		assert.Equal(t, "CID-FILL", *got.GooglePlaceID)
	})

	t.Run("degrades to a no-op when the place_id already belongs to another venue", func(t *testing.T) {
		owner := &entity.Venue{
			ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4b02",
			Name:            "Owner Hall",
			GooglePlaceID:   new("CID-TAKEN"),
			ListedVenueName: new("Owner Hall Listed"),
		}
		_, err := repo.Create(ctx, owner)
		require.NoError(t, err)

		orphan := &entity.Venue{
			ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4b03",
			Name:            "Orphan Hall",
			ListedVenueName: new("Orphan Hall Listed"),
		}
		_, err = repo.Create(ctx, orphan)
		require.NoError(t, err)

		// Backfilling the taken place_id would violate idx_venues_google_place_id;
		// the unique violation is swallowed and the orphan keeps its NULL place_id.
		require.NoError(t, repo.BackfillPlaceID(ctx, orphan.ID, "CID-TAKEN"))
		got, err := repo.Get(ctx, orphan.ID)
		require.NoError(t, err)
		assert.Nil(t, got.GooglePlaceID)
	})
}
