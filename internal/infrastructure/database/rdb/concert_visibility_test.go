package rdb_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// visibilityFixture builds the catalog rows the Concert read tests need:
// venues, artists, discovered events and first-party events in any state.
type visibilityFixture struct {
	t     *testing.T
	ctx   context.Context
	repo  *rdb.ConcertRepository
	orgID string
	today time.Time
}

func newVisibilityFixture(t *testing.T) *visibilityFixture {
	t.Helper()
	cleanDatabase(t)
	return &visibilityFixture{
		t:     t,
		ctx:   context.Background(),
		repo:  rdb.NewConcertRepository(testDB),
		orgID: seedOrganizer(t),
		today: time.Now().UTC().Truncate(24 * time.Hour),
	}
}

func (f *visibilityFixture) exec(sql string, args ...any) {
	f.t.Helper()
	_, err := testDB.Pool.Exec(f.ctx, sql, args...)
	require.NoError(f.t, err, sql)
}

// venue inserts a venue in adminArea, at lat/lng when given.
func (f *visibilityFixture) venue(adminArea string, lat, lng *float64) string {
	f.t.Helper()
	id := entity.NewID()
	f.exec(`INSERT INTO venues (id, name, admin_area, latitude, longitude) VALUES ($1, $2, $3, $4, $5)`,
		id, "Hall "+id[len(id)-6:], adminArea, lat, lng)
	return id
}

func (f *visibilityFixture) artist() string {
	f.t.Helper()
	return seedArtist(f.t, "Band "+uuid.NewV7().String()[30:], uuid.NewV4().String())
}

// event inserts one concert of the series at the venue on day (days from
// today), in the given state ("" for a discovered event), performed by the
// artists.
func (f *visibilityFixture) event(seriesID, venueID string, day int, state string, artistIDs ...string) string {
	f.t.Helper()
	id := entity.NewID()
	var st *string
	if state != "" {
		st = &state
	}
	f.exec(`INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state) VALUES ($1, $2, $3, $4, $5::event_publish_state)`,
		id, seriesID, venueID, f.today.AddDate(0, 0, day), st)
	f.exec(`INSERT INTO concerts (event_id) VALUES ($1)`, id)
	for _, a := range artistIDs {
		f.exec(`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`, id, a)
	}
	return id
}

func (f *visibilityFixture) discoveredSeries() string {
	f.t.Helper()
	id := entity.NewID()
	f.exec(`INSERT INTO series (id, title, type) VALUES ($1, 'Found Tour', 'TOUR')`, id)
	return id
}

func (f *visibilityFixture) firstPartySeries(visibility entity.SeriesVisibility) string {
	f.t.Helper()
	id := entity.NewID()
	f.exec(`INSERT INTO series (id, title, type, organizer_id, visibility) VALUES ($1, 'Organizer Tour', 'TOUR', $2, $3)`,
		id, f.orgID, string(visibility))
	return id
}

func (f *visibilityFixture) follower(artistIDs ...string) string {
	f.t.Helper()
	userID := seedUser(f.t, "Fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
	for _, a := range artistIDs {
		f.exec(`INSERT INTO followed_artists (user_id, artist_id) VALUES ($1, $2)`, userID, a)
	}
	return userID
}

func concertIDs(concerts []*entity.Concert) []string {
	ids := make([]string, len(concerts))
	for i, c := range concerts {
		ids[i] = c.ID
	}
	return ids
}

func eventIDs(events []*entity.Event) []string {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	return ids
}

func sorted(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

// TestConcertReads_NoDraftOrCancelledLeak runs every read of the Concert
// repository and the event publish port against one catalog holding a
// discovered event, a PUBLISHED event of a PUBLIC series, a PUBLISHED event of
// an UNLISTED series, a DRAFT and a CANCELLED event of that PUBLIC series and
// a DRAFT event at the discovered event's slot, and asserts the exact id set
// each one returns (design D4). A read method added to either interface
// without a row here fails the test.
func TestConcertReads_NoDraftOrCancelledLeak(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	f := newVisibilityFixture(t)
	eventState := rdb.NewEventPublishStateRepository(testDB)

	lat, lng := 35.6, 139.7
	venueID := f.venue("JP-13", &lat, &lng)
	artistID := f.artist()
	userID := f.follower(artistID)
	public := f.firstPartySeries(entity.SeriesVisibilityPublic)
	unlisted := f.firstPartySeries(entity.SeriesVisibilityUnlisted)

	discovered := f.event(f.discoveredSeries(), venueID, 10, "", artistID)
	published := f.event(public, venueID, 11, "PUBLISHED", artistID)
	unlistedPub := f.event(unlisted, venueID, 12, "PUBLISHED", artistID)
	draft := f.event(public, venueID, 13, "DRAFT", artistID)
	cancelled := f.event(public, venueID, 14, "CANCELLED", artistID)
	draftAtSlot := f.event(public, venueID, 10, "DRAFT", artistID)

	all := []string{discovered, published, unlistedPub, draft, cancelled, draftAtSlot}
	dates := make([]time.Time, 0, 5)
	for day := 10; day <= 14; day++ {
		dates = append(dates, f.today.AddDate(0, 0, day))
	}
	fanVisible := []string{discovered, published}
	catalog := []string{discovered, published, unlistedPub, cancelled}

	reads := map[string]struct {
		read func(t *testing.T) []string
		want []string
	}{
		"ListByArtist": {func(t *testing.T) []string {
			got, err := f.repo.ListByArtist(f.ctx, artistID, false)
			require.NoError(t, err)
			return concertIDs(got)
		}, fanVisible},
		"ListByFollower": {func(t *testing.T) []string {
			got, err := f.repo.ListByFollower(f.ctx, userID, &f.today)
			require.NoError(t, err)
			return concertIDs(got)
		}, fanVisible},
		"ListByArtists": {func(t *testing.T) []string {
			got, err := f.repo.ListByArtists(f.ctx, []string{artistID})
			require.NoError(t, err)
			return concertIDs(got)
		}, fanVisible},
		"ListByLocation": {func(t *testing.T) []string {
			got, err := f.repo.ListByLocation(f.ctx, &entity.GeoLocation{Latitude: lat, Longitude: lng, AdminArea: "JP-13"}, dates[0], dates[4])
			require.NoError(t, err)
			return concertIDs(got)
		}, fanVisible},
		"ListByIDs": {func(t *testing.T) []string {
			got, err := f.repo.ListByIDs(f.ctx, all)
			require.NoError(t, err)
			return concertIDs(got)
		}, catalog},
		"List": {func(t *testing.T) []string {
			got, err := f.repo.List(f.ctx)
			require.NoError(t, err)
			return concertIDs(got)
		}, catalog},
		"FindEventsByVenueAndDate": {func(t *testing.T) []string {
			venues := make([]string, len(dates))
			for i := range venues {
				venues[i] = venueID
			}
			got, err := f.repo.FindEventsByVenueAndDate(f.ctx, venues, dates)
			require.NoError(t, err)
			return eventIDs(got)
		}, catalog},
		"FindEventsByArtistAndDate": {func(t *testing.T) []string {
			got, err := f.repo.FindEventsByArtistAndDate(f.ctx, artistID, dates)
			require.NoError(t, err)
			return eventIDs(got)
		}, catalog},
		"ListEventsBySeries": {func(t *testing.T) []string {
			got, err := f.repo.ListEventsBySeries(f.ctx, public)
			require.NoError(t, err)
			return eventIDs(got)
		}, []string{published, cancelled}},
		"IsEventPublished": {func(t *testing.T) []string {
			var ids []string
			for _, id := range all {
				ok, err := eventState.IsEventPublished(f.ctx, id)
				require.NoError(t, err)
				if ok {
					ids = append(ids, id)
				}
			}
			return ids
		}, []string{published, unlistedPub}},
		"GetEventOrganizerID": {func(t *testing.T) []string {
			var ids []string
			for _, id := range all {
				org, err := eventState.GetEventOrganizerID(f.ctx, id)
				require.NoError(t, err)
				if org != "" {
					ids = append(ids, id)
				}
			}
			return ids
		}, []string{published, unlistedPub, draft, cancelled, draftAtSlot}},
	}

	t.Run("every read method of the Concert repository and the event publish port is covered", func(t *testing.T) {
		for _, iface := range []reflect.Type{
			reflect.TypeFor[entity.ConcertRepository](),
			reflect.TypeFor[usecase.EventPublishStatePort](),
		} {
			for method := range iface.Methods() {
				name := method.Name
				if strings.HasPrefix(name, "List") || strings.HasPrefix(name, "Find") ||
					strings.HasPrefix(name, "Get") || strings.HasPrefix(name, "Is") {
					_, ok := reads[name]
					assert.True(t, ok, "%s.%s reads events but has no row in this test", iface.Name(), name)
				}
			}
		}
	})

	for name, r := range reads {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, sorted(r.want), sorted(r.read(t)))
		})
	}

	t.Run("Draft not returned by id", func(t *testing.T) {
		// @spec components/entity/concert "Draft not returned by id"
		got, err := f.repo.ListByIDs(f.ctx, []string{draft})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("Discovery ignores a draft at the same slot", func(t *testing.T) {
		// @spec components/entity/concert "Discovery ignores a draft at the same slot"
		g := newVisibilityFixture(t)
		v := g.venue("JP-13", nil, nil)
		a := g.artist()
		draftOnly := g.event(g.firstPartySeries(entity.SeriesVisibilityPublic), v, 20, "DRAFT", a)
		found := g.discoveredSeries()
		start := g.today.AddDate(0, 0, 20).Add(9 * time.Hour)

		_, err := g.repo.Create(g.ctx, &entity.Concert{
			ID: entity.NewID(), SeriesID: found, VenueID: v, LocalDate: g.today.AddDate(0, 0, 20), StartTime: &start,
			Series: &entity.Series{ID: found}, Artists: []*entity.Artist{{ID: g.artist()}},
		})
		require.NoError(t, err)

		events, err := g.repo.FindEventsByVenueAndDate(g.ctx, []string{v}, []time.Time{g.today.AddDate(0, 0, 20)})
		require.NoError(t, err)
		require.Len(t, events, 1, "discovery adds its own event beside the draft")
		assert.NotEqual(t, draftOnly, events[0].ID)
		var draftStart *time.Time
		require.NoError(t, testDB.Pool.QueryRow(g.ctx, `SELECT start_at FROM events WHERE id = $1`, draftOnly).Scan(&draftStart))
		assert.Nil(t, draftStart, "the draft is left unchanged")
		assert.Equal(t, []string{a}, sorted(performersOf(t, g.ctx, draftOnly)))
	})
}

func performersOf(t *testing.T, ctx context.Context, eventID string) []string {
	t.Helper()
	rows, err := testDB.Pool.Query(ctx, `SELECT artist_id FROM concert_artists WHERE event_id = $1`, eventID)
	require.NoError(t, err)
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	return ids
}

func TestConcertRepository_List_PublishState(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	t.Run("return a CANCELLED organizer concert as CANCELLED", func(t *testing.T) {
		// @spec components/entity/concert/list "Cancelled organizer concert included"
		f := newVisibilityFixture(t)
		id := f.event(f.firstPartySeries(entity.SeriesVisibilityPublic), f.venue("JP-13", nil, nil), 5, "CANCELLED", f.artist())

		got, err := f.repo.List(f.ctx)

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, id, got[0].ID)
		assert.True(t, got[0].HasPublishState(entity.PublishStateCancelled))
	})

	t.Run("leave out a pending concert candidate", func(t *testing.T) {
		// @spec components/entity/concert/list "Concert candidate excluded"
		f := newVisibilityFixture(t)
		f.exec(`INSERT INTO concert_candidates (id, artist_id, series_id, title, local_date, listed_venue_name) VALUES ($1, $2, $3, 'Pending', $4, 'Somewhere')`,
			entity.NewID(), f.artist(), f.discoveredSeries(), f.today.AddDate(0, 0, 5))

		got, err := f.repo.List(f.ctx)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return an empty list for an empty catalog", func(t *testing.T) {
		// @spec components/entity/concert/list "Empty catalog"
		f := newVisibilityFixture(t)

		got, err := f.repo.List(f.ctx)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("leave out the DRAFT event of a published series", func(t *testing.T) {
		// @spec components/entity/concert/list "Draft event excluded"
		f := newVisibilityFixture(t)
		s := f.firstPartySeries(entity.SeriesVisibilityPublic)
		v := f.venue("JP-13", nil, nil)
		pub := f.event(s, v, 5, "PUBLISHED", f.artist())
		f.event(s, v, 6, "DRAFT", f.artist())

		got, err := f.repo.List(f.ctx)

		require.NoError(t, err)
		assert.Equal(t, []string{pub}, concertIDs(got))
	})
}

func TestConcertRepository_ListByArtist_Visibility(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	t.Run("return past and upcoming concerts, the past one first", func(t *testing.T) {
		// @spec components/entity/concert/list-by-artist "Past and upcoming"
		// @spec components/entity/concert/list-by-artist "Upcoming only"
		f := newVisibilityFixture(t)
		a, v, s := f.artist(), f.venue("JP-13", nil, nil), f.discoveredSeries()
		past := f.event(s, v, -3, "", a)
		future := f.event(s, v, 3, "", a)

		all, err := f.repo.ListByArtist(f.ctx, a, false)
		require.NoError(t, err)
		assert.Equal(t, []string{past, future}, concertIDs(all))

		upcoming, err := f.repo.ListByArtist(f.ctx, a, true)
		require.NoError(t, err)
		assert.Equal(t, []string{future}, concertIDs(upcoming))
	})

	t.Run("leave out a DRAFT organizer concert", func(t *testing.T) {
		// @spec components/entity/concert/list-by-artist "Draft organizer concert excluded"
		f := newVisibilityFixture(t)
		a := f.artist()
		f.event(f.firstPartySeries(entity.SeriesVisibilityPublic), f.venue("JP-13", nil, nil), 3, "DRAFT", a)

		got, err := f.repo.ListByArtist(f.ctx, a, false)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return an empty list for an artist with no concert", func(t *testing.T) {
		// @spec components/entity/concert/list-by-artist "No concerts"
		f := newVisibilityFixture(t)

		got, err := f.repo.ListByArtist(f.ctx, f.artist(), false)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return only the PUBLISHED date of a tour with DRAFT and CANCELLED dates", func(t *testing.T) {
		// @spec components/entity/concert/list-by-artist "Draft and cancelled dates of a published tour excluded"
		f := newVisibilityFixture(t)
		a, v, s := f.artist(), f.venue("JP-13", nil, nil), f.firstPartySeries(entity.SeriesVisibilityPublic)
		pub := f.event(s, v, 3, "PUBLISHED", a)
		f.event(s, v, 4, "DRAFT", a)
		f.event(s, v, 5, "CANCELLED", a)

		got, err := f.repo.ListByArtist(f.ctx, a, false)

		require.NoError(t, err)
		assert.Equal(t, []string{pub}, concertIDs(got))
	})
}

func TestConcertRepository_ListByArtists_Visibility(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	t.Run("return B's earlier concert before A's", func(t *testing.T) {
		// @spec components/entity/concert/list-by-artists "Two artists"
		f := newVisibilityFixture(t)
		a, b, v, s := f.artist(), f.artist(), f.venue("JP-13", nil, nil), f.discoveredSeries()
		ofA := f.event(s, v, 2, "", a)
		ofB := f.event(s, v, 1, "", b)

		got, err := f.repo.ListByArtists(f.ctx, []string{a, b})

		require.NoError(t, err)
		assert.Equal(t, []string{ofB, ofA}, concertIDs(got))
	})

	t.Run("leave out a CANCELLED organizer concert", func(t *testing.T) {
		// @spec components/entity/concert/list-by-artists "Cancelled organizer concert excluded"
		f := newVisibilityFixture(t)
		a := f.artist()
		f.event(f.firstPartySeries(entity.SeriesVisibilityPublic), f.venue("JP-13", nil, nil), 3, "CANCELLED", a)

		got, err := f.repo.ListByArtists(f.ctx, []string{a})

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return an empty list when no artist performs anywhere", func(t *testing.T) {
		// @spec components/entity/concert/list-by-artists "No concerts"
		f := newVisibilityFixture(t)

		got, err := f.repo.ListByArtists(f.ctx, []string{f.artist(), f.artist()})

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return only the PUBLISHED date of a tour with a DRAFT date", func(t *testing.T) {
		// @spec components/entity/concert/list-by-artists "Draft date of a published tour excluded"
		f := newVisibilityFixture(t)
		a, v, s := f.artist(), f.venue("JP-13", nil, nil), f.firstPartySeries(entity.SeriesVisibilityPublic)
		pub := f.event(s, v, 3, "PUBLISHED", a)
		f.event(s, v, 4, "DRAFT", a)

		got, err := f.repo.ListByArtists(f.ctx, []string{a})

		require.NoError(t, err)
		assert.Equal(t, []string{pub}, concertIDs(got))
	})
}

func TestConcertRepository_ListByFollower_Visibility(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	t.Run("start from today when no date is given", func(t *testing.T) {
		// @spec components/entity/concert/list-by-follower "Default start is today"
		f := newVisibilityFixture(t)
		a, v, s := f.artist(), f.venue("JP-13", nil, nil), f.discoveredSeries()
		f.event(s, v, -1, "", a)
		tomorrow := f.event(s, v, 1, "", a)

		got, err := f.repo.ListByFollower(f.ctx, f.follower(a), nil)

		require.NoError(t, err)
		assert.Equal(t, []string{tomorrow}, concertIDs(got))
	})

	t.Run("widen the range to a past start date", func(t *testing.T) {
		// @spec components/entity/concert/list-by-follower "Past start date widens the range"
		f := newVisibilityFixture(t)
		a, v, s := f.artist(), f.venue("JP-13", nil, nil), f.discoveredSeries()
		f.event(s, v, -8, "", a)
		weekAgo := f.event(s, v, -7, "", a)
		tomorrow := f.event(s, v, 1, "", a)
		from := f.today.AddDate(0, 0, -7)

		got, err := f.repo.ListByFollower(f.ctx, f.follower(a), &from)

		require.NoError(t, err)
		assert.Equal(t, []string{weekAgo, tomorrow}, concertIDs(got))
	})

	t.Run("return a concert with two followed performers once", func(t *testing.T) {
		// @spec components/entity/concert/list-by-follower "Two followed performers at one concert"
		f := newVisibilityFixture(t)
		a, b := f.artist(), f.artist()
		id := f.event(f.discoveredSeries(), f.venue("JP-13", nil, nil), 1, "", a, b)

		got, err := f.repo.ListByFollower(f.ctx, f.follower(a, b), nil)

		require.NoError(t, err)
		assert.Equal(t, []string{id}, concertIDs(got))
	})

	t.Run("leave out a PUBLISHED event of an UNLISTED series", func(t *testing.T) {
		// @spec components/entity/concert/list-by-follower "Unlisted organizer concert excluded"
		f := newVisibilityFixture(t)
		a := f.artist()
		f.event(f.firstPartySeries(entity.SeriesVisibilityUnlisted), f.venue("JP-13", nil, nil), 1, "PUBLISHED", a)

		got, err := f.repo.ListByFollower(f.ctx, f.follower(a), nil)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return an empty list for a user who follows nothing", func(t *testing.T) {
		// @spec components/entity/concert/list-by-follower "Follows nothing"
		f := newVisibilityFixture(t)
		f.event(f.discoveredSeries(), f.venue("JP-13", nil, nil), 1, "", f.artist())

		got, err := f.repo.ListByFollower(f.ctx, f.follower(), nil)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return only the PUBLISHED date of a tour with DRAFT and CANCELLED dates", func(t *testing.T) {
		// @spec components/entity/concert/list-by-follower "Draft and cancelled dates of a published tour excluded"
		f := newVisibilityFixture(t)
		a, v, s := f.artist(), f.venue("JP-13", nil, nil), f.firstPartySeries(entity.SeriesVisibilityPublic)
		pub := f.event(s, v, 1, "PUBLISHED", a)
		f.event(s, v, 2, "DRAFT", a)
		f.event(s, v, 3, "CANCELLED", a)

		got, err := f.repo.ListByFollower(f.ctx, f.follower(a), nil)

		require.NoError(t, err)
		assert.Equal(t, []string{pub}, concertIDs(got))
	})
}

func TestConcertRepository_ListByLocation_Visibility(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	// The point is Tokyo Station; Hamamatsu is about 210 km away and Shizuoka
	// about 150 km.
	tokyo := &entity.GeoLocation{Latitude: 35.681, Longitude: 139.767, AdminArea: "JP-13"}
	shizuokaLat, shizuokaLng := 34.975, 138.383
	sapporoLat, sapporoLng := 43.062, 141.354

	t.Run("return a concert in the admin area at a venue with no coordinates", func(t *testing.T) {
		// @spec components/entity/concert/list-by-location "Same admin area without coordinates"
		f := newVisibilityFixture(t)
		id := f.event(f.discoveredSeries(), f.venue("JP-13", nil, nil), 2, "", f.artist())

		got, err := f.repo.ListByLocation(f.ctx, tokyo, f.today, f.today.AddDate(0, 0, 7))

		require.NoError(t, err)
		assert.Equal(t, []string{id}, concertIDs(got))
	})

	t.Run("return a concert at a venue 150 km away in the range", func(t *testing.T) {
		// @spec components/entity/concert/list-by-location "Nearby venue in range"
		f := newVisibilityFixture(t)
		id := f.event(f.discoveredSeries(), f.venue("JP-22", &shizuokaLat, &shizuokaLng), 2, "", f.artist())

		got, err := f.repo.ListByLocation(f.ctx, tokyo, f.today, f.today.AddDate(0, 0, 7))

		require.NoError(t, err)
		assert.Equal(t, []string{id}, concertIDs(got))
	})

	t.Run("leave out a nearby concert the day after the range", func(t *testing.T) {
		// @spec components/entity/concert/list-by-location "Outside the date range"
		f := newVisibilityFixture(t)
		f.event(f.discoveredSeries(), f.venue("JP-22", &shizuokaLat, &shizuokaLng), 8, "", f.artist())

		got, err := f.repo.ListByLocation(f.ctx, tokyo, f.today, f.today.AddDate(0, 0, 7))

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return an empty list when nothing is near or in the admin area", func(t *testing.T) {
		// @spec components/entity/concert/list-by-location "Nothing nearby"
		f := newVisibilityFixture(t)
		f.event(f.discoveredSeries(), f.venue("JP-01", &sapporoLat, &sapporoLng), 2, "", f.artist())

		got, err := f.repo.ListByLocation(f.ctx, tokyo, f.today, f.today.AddDate(0, 0, 7))

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("leave out DRAFT and CANCELLED dates of a published PUBLIC tour", func(t *testing.T) {
		// @spec components/entity/concert/list-by-location "Draft and cancelled dates of a published tour excluded"
		f := newVisibilityFixture(t)
		a, v, s := f.artist(), f.venue("JP-13", nil, nil), f.firstPartySeries(entity.SeriesVisibilityPublic)
		pub := f.event(s, v, 1, "PUBLISHED", a)
		f.event(s, v, 2, "DRAFT", a)
		f.event(s, v, 3, "CANCELLED", a)

		got, err := f.repo.ListByLocation(f.ctx, tokyo, f.today, f.today.AddDate(0, 0, 7))

		require.NoError(t, err)
		assert.Equal(t, []string{pub}, concertIDs(got))
	})
}
