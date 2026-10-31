package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestConcertRepository_CreateAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewConcertRepository(testDB, testOutbox)
	seriesRepo := rdb.NewSeriesRepository(testDB, testOutbox)
	day := func(d int) time.Time { return time.Date(2026, 12, d, 0, 0, 0, 0, time.UTC) }

	// concertAt builds a Concert of the series at the venue on day d,
	// performed by the artists.
	concertAt := func(seriesID, venueID string, d int, artists ...string) *entity.Concert {
		c := &entity.Concert{ID: uuid.NewV7().String(), VenueID: venueID, SeriesID: seriesID, LocalDate: day(d)}
		for _, a := range artists {
			c.Artists = append(c.Artists, &entity.Artist{ID: a})
		}
		return c
	}
	newArtist := func(t *testing.T) string {
		t.Helper()
		return seedArtist(t, "artist "+uuid.NewV7().String(), uuid.NewV7().String())
	}

	t.Run("three new events of one tour", func(t *testing.T) {
		// @spec components/entity/concert/create "Three new events of one tour"
		artist, venue := newArtist(t), seedVenue(t, "venue "+uuid.NewV7().String())
		series := seedSeries(t, ctx, seriesRepo, "Tour")
		concerts := []*entity.Concert{concertAt(series, venue, 1, artist), concertAt(series, venue, 2, artist), concertAt(series, venue, 3, artist)}
		mark := outboxMark(t)

		_, err := repo.Create(ctx, concerts...)

		require.NoError(t, err)
		got := concertsAnnouncedFor(t, mark, artist)
		require.Len(t, got, 1)
		assert.ElementsMatch(t, []string{concerts[0].ID, concerts[1].ID, concerts[2].ID}, got[0])
	})

	t.Run("co-headliner found later", func(t *testing.T) {
		// @spec components/entity/concert/create "Co-headliner found later"
		a, b, venue := newArtist(t), newArtist(t), seedVenue(t, "venue "+uuid.NewV7().String())
		series := seedSeries(t, ctx, seriesRepo, "Show")
		first := concertAt(series, venue, 5, a)
		_, err := repo.Create(ctx, first)
		require.NoError(t, err)
		mark := outboxMark(t)

		_, err = repo.Create(ctx, concertAt(series, venue, 5, b))

		require.NoError(t, err)
		assert.Equal(t, [][]string{{first.ID}}, concertsAnnouncedFor(t, mark, b))
		assert.Empty(t, concertsAnnouncedFor(t, mark, a))
	})

	t.Run("repeated create", func(t *testing.T) {
		// @spec components/entity/concert/create "Repeated create"
		artist, venue := newArtist(t), seedVenue(t, "venue "+uuid.NewV7().String())
		series := seedSeries(t, ctx, seriesRepo, "Show")
		_, err := repo.Create(ctx, concertAt(series, venue, 7, artist))
		require.NoError(t, err)
		mark := outboxMark(t)

		_, err = repo.Create(ctx, concertAt(series, venue, 7, artist))

		require.NoError(t, err)
		assert.Empty(t, concertsAnnouncedFor(t, mark, artist))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/concert/create "Announcement cannot be recorded"
		artist, venue := newArtist(t), seedVenue(t, "venue "+uuid.NewV7().String())
		series := seedSeries(t, ctx, seriesRepo, "Show")
		c := concertAt(series, venue, 9, artist)

		_, err := rdb.NewConcertRepository(testDB, failingOutbox{}).Create(ctx, c)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		var n int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE series_id = $1`, series).Scan(&n))
		assert.Zero(t, n, "nothing is stored")
	})
}
