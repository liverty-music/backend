package rdb_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listFollowedIDs runs ConcertUseCase.ListFollowed over the fixture's real
// Concert repository, which is all it reads, and returns the ids of the
// Concerts in every group.
func listFollowedIDs(t *testing.T, f *visibilityFixture, userID string) []string {
	t.Helper()
	uc := usecase.NewConcertUseCase(nil, f.repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, 0, 0, time.Now, nil)

	groups, err := uc.ListFollowed(f.ctx, userID, &entity.Home{CountryCode: "JP", Level1: "JP-13"}, nil)

	require.NoError(t, err)
	var ids []string
	for _, g := range groups {
		for _, concerts := range [][]*entity.Concert{g.Home, g.Nearby, g.Away} {
			ids = append(ids, concertIDs(concerts)...)
		}
	}
	return ids
}

// @spec components/usecase/concert/list-followed "Today differs between venues"
func TestListFollowed_TodayDiffersBetweenVenues(t *testing.T) {
	t.Parallel()
	// Made outside the bubble: only the connections the test uses belong to
	// it.
	clone := testutil.NewDatabase(t)

	synctest.Test(t, func(t *testing.T) {
		// 2027-03-15 08:00 in Asia/Tokyo, 2027-03-14 23:00 in Europe/London.
		// The bubble's clock, which the repository reads, starts in 2000 and
		// jumps there.
		now := time.Date(2027, 3, 14, 23, 0, 0, 0, time.UTC)
		time.Sleep(time.Until(now))
		f := newVisibilityFixture(t, openTestDB(t, clone, 4))
		artist, series := f.artist(), f.discoveredSeries()
		tokyo, london := f.venue("JP-13", nil, nil), f.venue("GB-ENG", nil, nil)
		date := time.Date(2027, 3, 14, 0, 0, 0, 0, time.UTC)
		f.eventOn(series, tokyo, date, "", artist)
		inLondon := f.eventOn(series, london, date, "", artist)
		var zones []string
		require.NoError(t, f.db.Pool.QueryRow(f.ctx,
			`SELECT array_agg(time_zone ORDER BY time_zone) FROM venues WHERE id IN ($1, $2)`, tokyo, london).Scan(&zones))
		require.Equal(t, []string{"Asia/Tokyo", "Europe/London"}, zones)
		require.True(t, time.Now().Equal(now), "the setup took no time on the bubble's clock")

		got := listFollowedIDs(t, f, f.follower(artist))

		assert.Equal(t, []string{inLondon}, got)
	})
}

// @spec components/usecase/concert/list-followed "Cancelled Organizer concert"
func TestListFollowed_CancelledOrganizerConcert(t *testing.T) {
	t.Parallel()
	f := newVisibilityFixture(t, newTestDB(t))
	artist, venue := f.artist(), f.venue("JP-13", nil, nil)
	f.event(f.firstPartySeries(entity.SeriesVisibilityPublic), venue, 1, "CANCELLED", artist)
	// A Concert that stays, so the empty result is not an empty read.
	kept := f.event(f.discoveredSeries(), venue, 2, "", artist)

	got := listFollowedIDs(t, f, f.follower(artist))

	assert.Equal(t, []string{kept}, got)
}
