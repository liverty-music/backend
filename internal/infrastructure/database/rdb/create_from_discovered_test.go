package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// knownPlaces resolves the venues it lists and no other.
type knownPlaces map[string]*entity.VenuePlace

func (p knownPlaces) SearchPlace(_ context.Context, name, _ string) (*entity.VenuePlace, error) {
	return p[name], nil
}

// discoveredRowCounts returns the Events, Series and ConcertCandidates of
// artistID.
func discoveredRowCounts(t *testing.T, artistID string) (events, series, candidates int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, testDB.Pool.QueryRow(ctx,
		`SELECT count(*) FROM concert_artists WHERE artist_id = $1`, artistID).Scan(&events))
	require.NoError(t, testDB.Pool.QueryRow(ctx,
		`SELECT count(DISTINCT e.series_id) FROM events e JOIN concert_artists ca ON ca.event_id = e.id WHERE ca.artist_id = $1`, artistID).Scan(&series))
	require.NoError(t, testDB.Pool.QueryRow(ctx,
		`SELECT count(*) FROM concert_candidates WHERE artist_id = $1`, artistID).Scan(&candidates))
	return events, series, candidates
}

// TestCreateFromDiscovered_ProcessedAgain runs CreateFromDiscovered twice
// over the same batch, as a create-from-discovered workflow tried again after
// a failure does, against the real repositories.
//
// @spec components/usecase/concert/create-from-discovered "Processed again after a failure"
func TestCreateFromDiscovered_ProcessedAgain(t *testing.T) {
	cleanDatabase(t)
	ctx := context.Background()
	logger, err := logging.New()
	require.NoError(t, err)

	artistID := seedArtist(t, "Rerun Band", uuid.NewV4().String())
	suffix := uuid.NewV7().String()
	places := knownPlaces{
		"Zepp Haneda": {ProviderUserID: "ChIJ-haneda-" + suffix, Name: "Zepp Haneda", Coordinates: &entity.Coordinates{Latitude: 35.55, Longitude: 139.75}},
		"Zepp Namba":  {ProviderUserID: "ChIJ-namba-" + suffix, Name: "Zepp Namba", Coordinates: &entity.Coordinates{Latitude: 34.65, Longitude: 135.49}},
	}
	uc := usecase.NewConcertCreationUseCase(
		rdb.NewConcertCandidateRepository(testDB),
		rdb.NewVenueRepository(testDB),
		rdb.NewConcertRepository(testDB, testOutbox),
		rdb.NewSeriesRepository(testDB, testOutbox),
		rdb.NewSuppressedConcertRepository(testDB),
		places, nil, logger,
	)
	day := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, time.UTC)
	batch := func() entity.ConcertDiscoveredData {
		return entity.ConcertDiscoveredData{ArtistID: artistID, ArtistName: "Rerun Band", Series: []*entity.DiscoveredSeries{{
			Title: "Rerun Tour", Type: entity.SeriesTypeTour, SourceURL: "https://example.com/rerun",
			Events: []*entity.DiscoveredEvent{
				{ListedVenueName: "Zepp Haneda", LocalDate: day, StartTime: start},
				{ListedVenueName: "Zepp Namba", LocalDate: day.AddDate(0, 0, 2), StartTime: start.AddDate(0, 0, 2)},
				{ListedVenueName: "Unknown Hall", LocalDate: day.AddDate(0, 0, 4)},
			},
		}}}
	}

	require.NoError(t, uc.CreateFromDiscovered(ctx, batch()))
	events, series, candidates := discoveredRowCounts(t, artistID)
	require.Equal(t, []int{2, 1, 1}, []int{events, series, candidates}, "the first run publishes two events of one series and stages one")

	require.NoError(t, uc.CreateFromDiscovered(ctx, batch()))
	events, series, candidates = discoveredRowCounts(t, artistID)
	assert.Equal(t, []int{2, 1, 1}, []int{events, series, candidates}, "nothing is added a second time")
}
