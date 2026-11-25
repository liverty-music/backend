package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

func TestVenueRepository_Create(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewVenueRepository(db)
	ctx := context.Background()
	fetched := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)

	t.Run("Venue abroad", func(t *testing.T) {
		// @spec components/entity/venue/create "Venue abroad"
		// @spec components/entity/venue/create "New venue"
		v := &entity.Venue{ID: uuid.NewV7().String(), Name: "Gocheok Sky Dome", AdminArea: new("KR-11"), TimeZone: new("Asia/Seoul"), ListedVenueName: new("Gocheok Sky Dome")}
		id, err := repo.Create(ctx, v)
		require.NoError(t, err)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, "KR-11", *got.AdminArea)
		assert.Equal(t, "Asia/Seoul", *got.TimeZone)
	})
	t.Run("Coordinates with their fetch time", func(t *testing.T) {
		// @spec components/entity/venue/create "Coordinates with their fetch time"
		v := &entity.Venue{ID: uuid.NewV7().String(), Name: "Zepp Haneda(TOKYO)", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"),
			GooglePlaceID: new("ChIJhaneda"), Coordinates: &entity.Coordinates{Latitude: 35.55, Longitude: 139.78}, CoordinatesFetchTime: &fetched}
		id, err := repo.Create(ctx, v)
		require.NoError(t, err)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, v.Coordinates, got.Coordinates)
		assert.True(t, fetched.Equal(*got.CoordinatesFetchTime))
	})

	for _, tc := range []struct {
		name  string
		venue *entity.Venue
	}{
		// @spec components/entity/venue/create "No admin area"
		{name: "No admin area", venue: &entity.Venue{Name: "Hall", TimeZone: new("Asia/Tokyo")}},
		// @spec components/entity/venue/create "Lower-level code"
		{name: "Lower-level code", venue: &entity.Venue{Name: "Hall", AdminArea: new("FR-33"), TimeZone: new("Europe/Paris")}},
		// @spec components/entity/venue/create "No time zone"
		{name: "No time zone", venue: &entity.Venue{Name: "Hall", AdminArea: new("JP-13")}},
		// @spec components/entity/venue/create "Time zone of another country"
		{name: "Time zone of another country", venue: &entity.Venue{Name: "Hall", AdminArea: new("JP-13"), TimeZone: new("Asia/Seoul")}},
		// @spec components/entity/venue/create "Time zone other than the Area's own"
		{name: "Time zone other than the Area's own", venue: &entity.Venue{Name: "Hall", AdminArea: new("US-CA"), TimeZone: new("America/New_York")}},
		// @spec components/entity/venue/create "Coordinates without a fetch time"
		{name: "Coordinates without a fetch time", venue: &entity.Venue{Name: "Hall", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"),
			Coordinates: &entity.Coordinates{Latitude: 35, Longitude: 139}}},
		{name: "empty venue name", venue: &entity.Venue{Name: "", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.venue.ID = uuid.NewV7().String()
			_, err := repo.Create(ctx, tc.venue)
			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			_, err = repo.Get(ctx, tc.venue.ID)
			assert.ErrorIs(t, err, apperr.ErrNotFound, "nothing is stored")
		})
	}
}

func TestVenueRepository_Get(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewVenueRepository(db)
	ctx := context.Background()

	testVenue := &entity.Venue{
		ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
		AdminArea: new("JP-13"),
		TimeZone:  new("Asia/Tokyo"),
		Name:      "Get Test Arena",
	}
	{
		_, cErr := repo.Create(ctx, testVenue)
		require.NoError(t, cErr)
	}

	testVenueWithAdminArea := &entity.Venue{
		ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
		Name:      "Zepp Tokyo",
		AdminArea: new("JP-13"),
		TimeZone:  new("Asia/Tokyo"),
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
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e3",
				AdminArea: new("JP-13"),
				TimeZone:  new("Asia/Tokyo"),
				Name:      "Get Test Arena",
			},
		},
		{
			name: "get venue with admin_area",
			id:   "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
			want: &entity.Venue{
				ID:        "018b2f19-e591-7d12-bf9e-f0e74f1b49e6",
				Name:      "Zepp Tokyo",
				AdminArea: new("JP-13"),
				TimeZone:  new("Asia/Tokyo"),
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
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewVenueRepository(db)
	ctx := context.Background()

	budokan := &entity.Venue{ID: uuid.NewV7().String(), Name: "日本武道館", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: new("日本武道館")}
	haneda := &entity.Venue{ID: uuid.NewV7().String(), Name: "Zepp Haneda", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: new("Zepp Haneda")}
	clubX := &entity.Venue{ID: uuid.NewV7().String(), Name: "Club X", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: new("Club X")}
	for _, v := range []*entity.Venue{budokan, haneda, clubX} {
		_, err := repo.Create(ctx, v)
		require.NoError(t, err)
	}

	t.Run("Match", func(t *testing.T) {
		// @spec components/entity/venue/get-by-listed-name "Match"
		got, err := repo.GetByListedName(ctx, "日本武道館", "JP-13")
		require.NoError(t, err)
		assert.Equal(t, budokan.ID, got.ID)
	})
	t.Run("Different case misses", func(t *testing.T) {
		// @spec components/entity/venue/get-by-listed-name "Different case misses"
		_, err := repo.GetByListedName(ctx, "zepp haneda", "JP-13")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
	t.Run("Other admin area misses", func(t *testing.T) {
		// @spec components/entity/venue/get-by-listed-name "Other admin area misses"
		_, err := repo.GetByListedName(ctx, "Club X", "JP-27")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
	t.Run("No admin area given", func(t *testing.T) {
		// @spec components/entity/venue/get-by-listed-name "No admin area given"
		_, err := repo.GetByListedName(ctx, "Club X", "")
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestVenueRepository_GetByPlaceID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewVenueRepository(db)
	ctx := context.Background()

	// Seed a venue with a known GooglePlaceID so the happy-path test can find it.
	seededVenue := &entity.Venue{
		ID:            "018b2f19-e591-7d12-bf9e-f0e74f1b49eb",
		AdminArea:     new("JP-13"),
		TimeZone:      new("Asia/Tokyo"),
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
func countVenuesByListedName(t *testing.T, db *rdb.Database, ctx context.Context, listedName string) int {
	t.Helper()
	var n int
	err := db.Pool.QueryRow(ctx,
		"SELECT count(*) FROM venues WHERE listed_venue_name = $1", listedName).Scan(&n)
	require.NoError(t, err)
	return n
}

func TestVenueRepository_Create_GetOrCreate(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewVenueRepository(db)
	ctx := context.Background()

	// A physical venue that Google Places resolved with two different CIDs across
	// discovery runs (the observed 和歌山ビッグホエール case).
	const listedName = "和歌山ビッグホエール"
	existing := &entity.Venue{
		ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4a01",
		Name:            "Wakayama Big Whale",
		AdminArea:       new("JP-30"),
		TimeZone:        new("Asia/Tokyo"),
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
			TimeZone:        new("Asia/Tokyo"),
			GooglePlaceID:   new("CID-B"),
			ListedVenueName: new(listedName),
		})
		require.NoError(t, err)
		assert.Equal(t, existing.ID, gotID)
		assert.Equal(t, 1, countVenuesByListedName(t, db, ctx, listedName))
	})
}

func TestVenueRepository_Create_ConcurrentSingleRow(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewVenueRepository(db)
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
				ID:              uuid.NewV7().String(),
				Name:            "Concurrent Hall Canonical",
				AdminArea:       new("JP-13"),
				TimeZone:        new("Asia/Tokyo"),
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
	assert.Equal(t, 1, countVenuesByListedName(t, db, ctx, listedName))
}

func TestVenueRepository_BackfillPlaceID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewVenueRepository(db)
	ctx := context.Background()

	t.Run("fills a NULL place_id and never overwrites a non-NULL one", func(t *testing.T) {
		v := &entity.Venue{
			ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4b01",
			AdminArea:       new("JP-13"),
			TimeZone:        new("Asia/Tokyo"),
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
			AdminArea:       new("JP-13"),
			TimeZone:        new("Asia/Tokyo"),
			Name:            "Owner Hall",
			GooglePlaceID:   new("CID-TAKEN"),
			ListedVenueName: new("Owner Hall Listed"),
		}
		_, err := repo.Create(ctx, owner)
		require.NoError(t, err)

		orphan := &entity.Venue{
			ID:              "018b2f19-e591-7d12-bf9e-f0e74f1b4b03",
			AdminArea:       new("JP-13"),
			TimeZone:        new("Asia/Tokyo"),
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
