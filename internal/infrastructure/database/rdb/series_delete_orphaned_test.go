package rdb_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestSeriesRepository_DeleteOrphaned_Requirements(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// A Series a fixture creates now counts as created before an hour from
	// now, and not before a day ago.
	old, recent := func() time.Time { return time.Now().Add(time.Hour) }, func() time.Time { return time.Now().Add(-24 * time.Hour) }

	// remove calls DeleteOrphaned with createdBefore and reports how many
	// Series it removed.
	remove := func(f *visibilityFixture, createdBefore time.Time) int64 {
		f.t.Helper()
		n, err := rdb.NewSeriesRepository(db, testOutbox).DeleteOrphaned(f.ctx, createdBefore)
		require.NoError(f.t, err)
		return n
	}
	kept := func(f *visibilityFixture, id string) bool {
		f.t.Helper()
		return f.count(`SELECT count(*) FROM series WHERE id = $1`, id) == 1
	}
	sale := func(f *visibilityFixture) string {
		return `INSERT INTO ticket_sales (id, series_id, name, method, time_zone, local_start_time, local_end_time, find_time)
			VALUES ($1, $2, 'FC先行', 2, 'Asia/Tokyo', '2026-12-01 12:00', '2026-12-05 23:59', now())`
	}

	t.Run("Empty series removed", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Empty series removed"
		f := newVisibilityFixture(t, db)
		s := f.discoveredSeries()

		assert.GreaterOrEqual(t, remove(f, old()), int64(1))
		assert.False(t, kept(f, s))
	})

	t.Run("Recent series kept", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Recent series kept"
		f := newVisibilityFixture(t, db)
		s := f.discoveredSeries()

		remove(f, recent())

		assert.True(t, kept(f, s))
	})

	t.Run("Series with a concert candidate kept", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Series with a concert candidate kept"
		f := newVisibilityFixture(t, db)
		s := f.discoveredSeries()
		f.exec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, listed_venue_name, local_date)
			VALUES ($1, $2, $3, 'Found Tour', 'Zepp Haneda', $4)`, entity.NewID(), f.artist(), s, slotDate)

		remove(f, old())

		assert.True(t, kept(f, s))
	})

	t.Run("Series with a ticket sale kept", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Series with a ticket sale kept"
		f := newVisibilityFixture(t, db)
		s := f.discoveredSeries()
		f.exec(sale(f), entity.NewID(), s)

		remove(f, old())

		assert.True(t, kept(f, s))
	})

	t.Run("Series still being written", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Series still being written"
		f := newVisibilityFixture(t, db)
		s := f.discoveredSeries()
		tx, err := db.Pool.Begin(f.ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(f.ctx) }()
		_, err = tx.Exec(f.ctx, sale(f), entity.NewID(), s)
		require.NoError(t, err)

		remove(f, old())
		require.NoError(t, tx.Commit(f.ctx))

		assert.True(t, kept(f, s), "the removal does not wait for the sale and leaves its Series")
	})

	t.Run("Organizer's series kept", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Organizer's series kept"
		f := newVisibilityFixture(t, db)
		s := f.firstPartySeries(entity.SeriesVisibilityPublic)

		remove(f, old())

		assert.True(t, kept(f, s))
	})
}
