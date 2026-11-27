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

	// remove calls DeleteOrphaned and reports how many Series it removed.
	remove := func(f *visibilityFixture, ids ...string) int64 {
		f.t.Helper()
		n, err := rdb.NewSeriesRepository(db, testOutbox).DeleteOrphaned(f.ctx, ids)
		assert.NoError(f.t, err)
		return n
	}
	kept := func(f *visibilityFixture, id string) bool {
		f.t.Helper()
		return f.count(`SELECT count(*) FROM series WHERE id = $1`, id) == 1
	}

	t.Run("Empty series removed", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Empty series removed"
		f := newVisibilityFixture(t, db)
		s := f.discoveredSeries()

		assert.Equal(t, int64(1), remove(f, s))
		assert.False(t, kept(f, s))
	})

	t.Run("Series with a concert candidate kept", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Series with a concert candidate kept"
		f := newVisibilityFixture(t, db)
		s := f.discoveredSeries()
		f.exec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, listed_venue_name, local_date)
			VALUES ($1, $2, $3, 'Found Tour', 'Zepp Haneda', $4)`, entity.NewID(), f.artist(), s, slotDate)

		assert.Equal(t, int64(0), remove(f, s))
		assert.True(t, kept(f, s))
	})

	t.Run("Series with a ticket sale kept", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Series with a ticket sale kept"
		f := newVisibilityFixture(t, db)
		s := f.discoveredSeries()
		f.exec(`INSERT INTO ticket_sales (id, series_id, name, method, time_zone, local_start_time, local_end_time, find_time)
			VALUES ($1, $2, 'FC先行', 2, 'Asia/Tokyo', '2026-12-01 12:00', '2026-12-05 23:59', now())`, entity.NewID(), s)

		assert.Equal(t, int64(0), remove(f, s))
		assert.True(t, kept(f, s))
	})

	t.Run("a ticket sale committed while the sweep waits keeps the series", func(t *testing.T) {
		f := newVisibilityFixture(t, db)
		s := f.discoveredSeries()
		sale, err := db.Pool.Begin(f.ctx)
		require.NoError(t, err)
		defer func() { _ = sale.Rollback(f.ctx) }()
		_, err = sale.Exec(f.ctx, `INSERT INTO ticket_sales (id, series_id, name, method, time_zone, local_start_time, local_end_time, find_time)
			VALUES ($1, $2, 'FC先行', 2, 'Asia/Tokyo', '2026-12-01 12:00', '2026-12-05 23:59', now())`, entity.NewID(), s)
		require.NoError(t, err)

		removed := make(chan int64, 1)
		go func() { removed <- remove(f, s) }()
		require.Eventually(t, func() bool {
			return f.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'SELECT id FROM series%'`) > 0
		}, 5*time.Second, 10*time.Millisecond, "the sweep waits for the sale's key-share lock")
		require.NoError(t, sale.Commit(f.ctx))

		assert.Equal(t, int64(0), <-removed)
		assert.True(t, kept(f, s))
	})

	t.Run("Series not given", func(t *testing.T) {
		// @spec components/entity/series/delete-orphaned "Series not given"
		f := newVisibilityFixture(t, db)
		given, other := f.discoveredSeries(), f.discoveredSeries()

		remove(f, given)

		assert.True(t, kept(f, other))
	})
}
