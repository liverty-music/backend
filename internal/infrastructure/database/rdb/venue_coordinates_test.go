package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// coordinatesInstant is the instant every coordinates test runs at.
var coordinatesInstant = time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)

// createVenueWithCoordinates stores a Venue in JP-13 with the given place id
// (none when empty) and, when fetch is not nil, coordinates fetched then.
func createVenueWithCoordinates(t *testing.T, repo *rdb.VenueRepository, placeID string, fetch *time.Time) string {
	t.Helper()
	v := &entity.Venue{ID: uuid.NewV7().String(), Name: "Hall " + placeID, AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo")}
	if placeID != "" {
		v.GooglePlaceID = &placeID
	}
	if fetch != nil {
		v.Coordinates = &entity.Coordinates{Latitude: 35.55, Longitude: 139.78}
		v.CoordinatesFetchTime = fetch
	}
	id, err := repo.Create(context.Background(), v)
	require.NoError(t, err)
	return id
}

// fetchedDaysBefore is the instant n days before coordinatesInstant.
func fetchedDaysBefore(n int) *time.Time {
	t := coordinatesInstant.Add(-time.Duration(n) * 24 * time.Hour)
	return &t
}

func TestVenueRepository_SetCoordinates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/entity/venue/set-coordinates "Refreshed coordinates"
	t.Run("replace the coordinates and their fetch time", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))
		old := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
		id := createVenueWithCoordinates(t, repo, "ChIJrefresh", &old)

		fresh := &entity.Coordinates{Latitude: 35.56, Longitude: 139.79}
		require.NoError(t, repo.SetCoordinates(ctx, id, &entity.PlaceCoordinates{Coordinates: fresh, FetchTime: coordinatesInstant}))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, fresh, got.Coordinates)
		assert.True(t, coordinatesInstant.Equal(*got.CoordinatesFetchTime))
		assert.Equal(t, "JP-13", *got.AdminArea)
		assert.Equal(t, "Asia/Tokyo", *got.TimeZone)
	})

	// @spec components/entity/venue/set-coordinates "Coordinates removed"
	t.Run("remove the coordinates and their fetch time and keep the place id", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))
		id := createVenueWithCoordinates(t, repo, "ChIJgone", fetchedDaysBefore(3))

		require.NoError(t, repo.SetCoordinates(ctx, id, nil))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Nil(t, got.Coordinates)
		assert.Nil(t, got.CoordinatesFetchTime)
		assert.Equal(t, "ChIJgone", *got.GooglePlaceID)
	})

	// @spec components/entity/venue/set-coordinates "Unknown venue"
	t.Run("fail with NOT_FOUND when no venue has the id", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))

		err := repo.SetCoordinates(ctx, uuid.NewV7().String(), nil)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("fail with INVALID_ARGUMENT when given a fetch time without coordinates", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))
		id := createVenueWithCoordinates(t, repo, "ChIJkeep", fetchedDaysBefore(3))

		err := repo.SetCoordinates(ctx, id, &entity.PlaceCoordinates{FetchTime: coordinatesInstant})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestVenueRepository_ListCoordinatesDue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ids := func(venues []*entity.Venue) []string {
		out := make([]string, 0, len(venues))
		for _, v := range venues {
			out = append(out, v.ID)
		}
		return out
	}

	// @spec components/entity/venue/list-coordinates-due "Coordinates older than 20 days"
	// @spec components/entity/venue/list-coordinates-due "Coordinates younger than 20 days"
	// @spec components/entity/venue/list-coordinates-due "Missing coordinates"
	// @spec components/entity/venue/list-coordinates-due "No place id"
	t.Run("list missing coordinates first, then those older than 20 days, and no venue without a place id", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))
		old := createVenueWithCoordinates(t, repo, "ChIJold", new(time.Date(2026, 11, 3, 4, 0, 0, 0, time.UTC)))
		createVenueWithCoordinates(t, repo, "ChIJyoung", new(time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC)))
		missing := createVenueWithCoordinates(t, repo, "ChIJmissing", nil)
		createVenueWithCoordinates(t, repo, "", nil)

		got, err := repo.ListCoordinatesDue(ctx, coordinatesInstant, 10)

		require.NoError(t, err)
		assert.Equal(t, []string{missing, old}, ids(got))
		assert.Equal(t, "ChIJmissing", *got[0].GooglePlaceID)
	})

	// @spec components/entity/venue/list-coordinates-due "More due than the maximum"
	t.Run("return at most the maximum count, oldest first", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))
		createVenueWithCoordinates(t, repo, "ChIJd21", fetchedDaysBefore(21))
		d25 := createVenueWithCoordinates(t, repo, "ChIJd25", fetchedDaysBefore(25))
		d23 := createVenueWithCoordinates(t, repo, "ChIJd23", fetchedDaysBefore(23))

		got, err := repo.ListCoordinatesDue(ctx, coordinatesInstant, 2)

		require.NoError(t, err)
		assert.Equal(t, []string{d25, d23}, ids(got))
	})

	// @spec components/entity/venue/list-coordinates-due "Nothing due"
	t.Run("return an empty list when nothing is due", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))
		createVenueWithCoordinates(t, repo, "ChIJfresh", fetchedDaysBefore(1))

		got, err := repo.ListCoordinatesDue(ctx, coordinatesInstant, 10)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("fail with INVALID_ARGUMENT for a maximum count below 1", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))

		_, err := repo.ListCoordinatesDue(ctx, coordinatesInstant, 0)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestVenueRepository_ClearExpiredCoordinates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/entity/venue/clear-expired-coordinates "Expired coordinates removed"
	// @spec components/entity/venue/clear-expired-coordinates "Recent coordinates kept"
	t.Run("remove coordinates older than 30 days and keep the rest", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))
		expired := createVenueWithCoordinates(t, repo, "ChIJexpired", new(time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)))
		recent := createVenueWithCoordinates(t, repo, "ChIJrecent", new(time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC)))

		n, err := repo.ClearExpiredCoordinates(ctx, coordinatesInstant)

		require.NoError(t, err)
		assert.Equal(t, int64(1), n)
		got, err := repo.Get(ctx, expired)
		require.NoError(t, err)
		assert.Nil(t, got.Coordinates)
		assert.Nil(t, got.CoordinatesFetchTime)
		assert.Equal(t, "ChIJexpired", *got.GooglePlaceID)
		got, err = repo.Get(ctx, recent)
		require.NoError(t, err)
		assert.NotNil(t, got.Coordinates)
		assert.NotNil(t, got.CoordinatesFetchTime)

		again, err := repo.ClearExpiredCoordinates(ctx, coordinatesInstant)
		require.NoError(t, err)
		assert.Zero(t, again, "running again changes nothing more")
	})

	// @spec components/entity/venue/clear-expired-coordinates "Nothing expired"
	t.Run("change no venue when nothing has expired", func(t *testing.T) {
		t.Parallel()
		repo := rdb.NewVenueRepository(newTestDB(t))
		id := createVenueWithCoordinates(t, repo, "ChIJfresh", fetchedDaysBefore(29))

		n, err := repo.ClearExpiredCoordinates(ctx, coordinatesInstant)

		require.NoError(t, err)
		assert.Zero(t, n)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.NotNil(t, got.Coordinates)
	})
}

func TestVenueRepository_ListWithoutAreaOrZone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	repo := rdb.NewVenueRepository(db)
	insert := func(name string, listed, area, zone *string) string {
		t.Helper()
		id := uuid.NewV7().String()
		_, err := db.Pool.Exec(ctx, `INSERT INTO venues (id, name, listed_venue_name, admin_area, time_zone) VALUES ($1, $2, $3, $4, $5)`,
			id, name, listed, area, zone)
		require.NoError(t, err)
		return id
	}
	complete := insert("Zepp Haneda", new("Zepp Haneda"), new("JP-13"), new("Asia/Tokyo"))
	noZone := insert("Stubb's BBQ", new("Stubb's"), new("US-TX"), nil)
	noArea := insert("Legacy Hall", nil, nil, nil)
	artist := seedArtist(t, db, "Backfill Artist", "a5000000-0000-0000-0000-0000000000b1")
	oldEvent := seedEvent(t, db, noZone, artist, "Old Show", "2026-01-10")
	newEvent := seedEvent(t, db, noZone, artist, "New Show", "2026-06-10")
	seedEvent(t, db, complete, artist, "Tokyo Show", "2026-06-11")
	for event, url := range map[string]string{oldEvent: "https://example.com/old", newEvent: "https://example.com/new"} {
		_, err := db.Pool.Exec(ctx, `UPDATE series SET source_url = $1 WHERE id = (SELECT series_id FROM events WHERE id = $2)`, url, event)
		require.NoError(t, err)
	}

	got, err := repo.ListWithoutAreaOrZone(ctx)

	require.NoError(t, err)
	byID := map[string]*entity.VenueToBackfill{}
	for _, v := range got {
		byID[v.VenueID] = v
	}
	assert.Len(t, got, 2, "the venue with an area and a zone is left out")
	assert.Equal(t, &entity.VenueToBackfill{VenueID: noZone, ListedName: "Stubb's", AdminArea: "US-TX", SourceURL: "https://example.com/new"}, byID[noZone],
		"the listed name and the latest event's source page")
	assert.Equal(t, &entity.VenueToBackfill{VenueID: noArea, ListedName: "Legacy Hall"}, byID[noArea],
		"the name when there is no listed name, and no source page")
}
