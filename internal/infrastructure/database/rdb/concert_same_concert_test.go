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

// slotEvent describes one event a same-concert test stores.
type slotEvent struct {
	series, venue string
	date          time.Time
	// start is the HH:MM start time; "" when unknown.
	start string
	// listed is the listed venue name; "" for none.
	listed string
	others []string
	// state is the publish state; "" for a discovered event.
	state   string
	artists []string
}

// store inserts the event with its concert row and performers and returns
// its id.
func (f *visibilityFixture) store(e slotEvent) string {
	f.t.Helper()
	id := entity.NewID()
	var start, listed, state *string
	if e.start != "" {
		start = &e.start
	}
	if e.listed != "" {
		listed = &e.listed
	}
	if e.state != "" {
		state = &e.state
	}
	others := e.others
	if others == nil {
		others = []string{}
	}
	f.exec(`INSERT INTO events (id, series_id, venue_id, local_event_date, local_start_time, listed_venue_name, other_listed_venue_names, publish_state, per_account_limit)
		VALUES ($1, $2, $3, $4, $5::text::time, $6, $7, $8::event_publish_state, CASE WHEN $8::event_publish_state IS NULL THEN NULL ELSE 4 END)`,
		id, e.series, e.venue, e.date, start, listed, others, state)
	f.exec(`INSERT INTO concerts (event_id) VALUES ($1)`, id)
	for _, a := range e.artists {
		f.exec(`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`, id, a)
	}
	return id
}

// eventRow is what a same-concert test reads back of one event.
type eventRow struct {
	exists     bool
	listed     *string
	others     []string
	start      *string
	performers []string
}

func (f *visibilityFixture) read(id string) eventRow {
	f.t.Helper()
	var r eventRow
	err := f.db.Pool.QueryRow(f.ctx, `SELECT listed_venue_name, other_listed_venue_names, to_char(local_start_time, 'HH24:MI'),
		ARRAY(SELECT artist_id::text FROM concert_artists WHERE event_id = e.id ORDER BY artist_id)
		FROM events e WHERE id = $1`, id).Scan(&r.listed, &r.others, &r.start, &r.performers)
	if err == nil {
		r.exists = true
	}
	return r
}

func (f *visibilityFixture) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	require.NoError(f.t, f.db.Pool.QueryRow(f.ctx, sql, args...).Scan(&n), sql)
	return n
}

func (f *visibilityFixture) fan() string {
	f.t.Helper()
	return seedUser(f.t, f.db, "Fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
}

var (
	slotDate = time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC)
	nextDate = time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)
)

func performerIDs(c *entity.Concert) []string {
	ids := c.ArtistIDs()
	return ids
}

func TestConcertRepository_FindEventsByArtistAndDate(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("Artist performs on one of the dates", func(t *testing.T) {
		// @spec components/entity/concert/find-events-by-artist-and-date "Artist performs on one of the dates"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		id := f.store(slotEvent{series: s, venue: v, date: slotDate, listed: "Zepp Haneda", artists: []string{a}})

		got, err := f.repo.FindEventsByArtistAndDate(f.ctx, a, []time.Time{slotDate, nextDate})
		require.NoError(t, err)
		assert.Equal(t, []string{id}, concertIDs(got))
	})

	t.Run("Other artist's event on the date", func(t *testing.T) {
		// @spec components/entity/concert/find-events-by-artist-and-date "Other artist's event on the date"
		f := newVisibilityFixture(t, db)
		a, other, s, v := f.artist(), f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		f.store(slotEvent{series: s, venue: v, date: slotDate, artists: []string{other}})

		got, err := f.repo.FindEventsByArtistAndDate(f.ctx, a, []time.Time{slotDate})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("Other listed venue names returned", func(t *testing.T) {
		// @spec components/entity/concert/find-events-by-artist-and-date "Other listed venue names returned"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-27", nil, nil)
		f.store(slotEvent{series: s, venue: v, date: slotDate, start: "17:00", listed: "京セラドーム大阪", others: []string{"Kyocera Dome Osaka"}, artists: []string{a}})

		got, err := f.repo.FindEventsByArtistAndDate(f.ctx, a, []time.Time{slotDate})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NotNil(t, got[0].ListedVenueName)
		assert.Equal(t, "京セラドーム大阪", *got[0].ListedVenueName)
		assert.Equal(t, []string{"Kyocera Dome Osaka"}, got[0].OtherListedVenueNames)
		assert.Equal(t, s, got[0].SeriesID)
		assert.Equal(t, v, got[0].VenueID)
		assert.Equal(t, &entity.TimeOfDay{Hour: 17}, got[0].StartTime)
		assert.Nil(t, got[0].PublishState, "a discovered event has no publish state")
	})

	t.Run("Shared bill returned with its performers", func(t *testing.T) {
		// @spec components/entity/concert/find-events-by-artist-and-date "Shared bill returned with its performers"
		f := newVisibilityFixture(t, db)
		a, b, v := f.artist(), f.artist(), f.venue("JP-13", nil, nil)
		s := f.firstPartySeries(entity.SeriesVisibilityPublic)
		f.store(slotEvent{series: s, venue: v, date: slotDate, start: "18:00", state: "PUBLISHED", artists: []string{a, b}})

		got, err := f.repo.FindEventsByArtistAndDate(f.ctx, a, []time.Time{slotDate})
		require.NoError(t, err)
		require.Len(t, got, 1, "the event is returned once although two artists perform")
		assert.ElementsMatch(t, []string{a, b}, performerIDs(got[0]))
		require.NotNil(t, got[0].PublishState)
		assert.Equal(t, entity.PublishStatePublished, *got[0].PublishState)
	})
}

func TestConcertRepository_FindEventsByVenueAndDate(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("Two shows on one date", func(t *testing.T) {
		// @spec components/entity/concert/find-events-by-venue-and-date "Two shows on one date"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		matinee := f.store(slotEvent{series: s, venue: v, date: slotDate, start: "13:00", artists: []string{a}})
		evening := f.store(slotEvent{series: s, venue: v, date: slotDate, start: "18:00", artists: []string{a}})

		got, err := f.repo.FindEventsByVenueAndDate(f.ctx, []string{v, v}, []time.Time{slotDate, slotDate})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{matinee, evening}, concertIDs(got), "each event once, though the pair is given twice")
	})

	t.Run("No match", func(t *testing.T) {
		// @spec components/entity/concert/find-events-by-venue-and-date "No match"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		f.store(slotEvent{series: s, venue: v, date: slotDate, artists: []string{a}})

		got, err := f.repo.FindEventsByVenueAndDate(f.ctx, []string{v}, []time.Time{nextDate})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("Performers returned", func(t *testing.T) {
		// @spec components/entity/concert/find-events-by-venue-and-date "Performers returned"
		f := newVisibilityFixture(t, db)
		a, b, s, v := f.artist(), f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		f.store(slotEvent{series: s, venue: v, date: slotDate, start: "18:00", listed: "Zepp Haneda(TOKYO)", others: []string{"Zepp Haneda"}, artists: []string{a, b}})

		got, err := f.repo.FindEventsByVenueAndDate(f.ctx, []string{v}, []time.Time{slotDate})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.ElementsMatch(t, []string{a, b}, performerIDs(got[0]))
		require.NotNil(t, got[0].ListedVenueName)
		assert.Equal(t, "Zepp Haneda(TOKYO)", *got[0].ListedVenueName)
		assert.Equal(t, []string{"Zepp Haneda"}, got[0].OtherListedVenueNames)
	})

	t.Run("Organizer's event returned with its publish state", func(t *testing.T) {
		// @spec components/entity/concert/find-events-by-venue-and-date "Organizer's event returned with its publish state"
		f := newVisibilityFixture(t, db)
		a, v := f.artist(), f.venue("JP-13", nil, nil)
		s := f.firstPartySeries(entity.SeriesVisibilityPublic)
		f.store(slotEvent{series: s, venue: v, date: slotDate, start: "18:00", state: "CANCELLED", artists: []string{a}})

		got, err := f.repo.FindEventsByVenueAndDate(f.ctx, []string{v}, []time.Time{slotDate})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NotNil(t, got[0].PublishState)
		assert.Equal(t, entity.PublishStateCancelled, *got[0].PublishState)
	})
}

func TestConcertRepository_FillEventStartTimes(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	at := func(h int) []*entity.TimeOfDay { return []*entity.TimeOfDay{{Hour: h}} }

	t.Run("Unknown start filled", func(t *testing.T) {
		// @spec components/entity/concert/fill-event-start-times "Unknown start filled"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		id := f.store(slotEvent{series: s, venue: v, date: slotDate, artists: []string{a}})

		require.NoError(t, f.repo.FillEventStartTimes(f.ctx, []string{id}, at(18), []*entity.TimeOfDay{nil}))
		assert.Equal(t, new("18:00"), f.read(id).start)
	})

	t.Run("Known start kept", func(t *testing.T) {
		// @spec components/entity/concert/fill-event-start-times "Known start kept"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		id := f.store(slotEvent{series: s, venue: v, date: slotDate, start: "18:00", artists: []string{a}})

		require.NoError(t, f.repo.FillEventStartTimes(f.ctx, []string{id}, at(19), []*entity.TimeOfDay{nil}))
		assert.Equal(t, new("18:00"), f.read(id).start)
	})

	t.Run("Repeated fill", func(t *testing.T) {
		// @spec components/entity/concert/fill-event-start-times "Repeated fill"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		id := f.store(slotEvent{series: s, venue: v, date: slotDate, artists: []string{a}})

		require.NoError(t, f.repo.FillEventStartTimes(f.ctx, []string{id}, at(18), []*entity.TimeOfDay{nil}))
		require.NoError(t, f.repo.FillEventStartTimes(f.ctx, []string{id}, at(18), []*entity.TimeOfDay{nil}))
		assert.Equal(t, new("18:00"), f.read(id).start)
	})

	t.Run("Slot held by another event", func(t *testing.T) {
		// @spec components/entity/concert/fill-event-start-times "Slot held by another event"
		f := newVisibilityFixture(t, db)
		a, b, s, v := f.artist(), f.artist(), f.discoveredSeries(), f.venue("JP-27", nil, nil)
		unknown := f.store(slotEvent{series: s, venue: v, date: slotDate, artists: []string{a}})
		other := f.store(slotEvent{series: s, venue: v, date: nextDate, artists: []string{a}})
		f.store(slotEvent{series: s, venue: v, date: slotDate, start: "17:00", artists: []string{b}})

		err := f.repo.FillEventStartTimes(f.ctx, []string{other, unknown}, append(at(17), &entity.TimeOfDay{Hour: 17}), []*entity.TimeOfDay{nil, nil})
		require.ErrorIs(t, err, apperr.ErrAlreadyExists)
		assert.Nil(t, f.read(unknown).start, "the event still has no start time")
		assert.Nil(t, f.read(other).start, "no other given event is changed")
	})
}

func TestConcertRepository_UpdateEventListedVenueNameKeepsTheReplacedName(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("Name replaced", func(t *testing.T) {
		// @spec components/entity/concert/update-event-listed-venue-name "Name replaced"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		id := f.store(slotEvent{series: s, venue: v, date: slotDate, start: "18:00", listed: "Zepp", artists: []string{a}})

		require.NoError(t, f.repo.UpdateEventListedVenueName(f.ctx, id, "Zepp DiverCity"))
		got := f.read(id)
		assert.Equal(t, new("Zepp DiverCity"), got.listed)
		assert.Equal(t, []string{"Zepp"}, got.others, "still listed under the replaced name")
		assert.Equal(t, 1, f.count(`SELECT COUNT(*) FROM events WHERE id = $1 AND venue_id = $2`, id, v), "the Venue is unchanged")
	})

	t.Run("Repeated update", func(t *testing.T) {
		// @spec components/entity/concert/update-event-listed-venue-name "Repeated update"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-13", nil, nil)
		id := f.store(slotEvent{series: s, venue: v, date: slotDate, listed: "Zepp DiverCity", others: []string{"Zepp"}, artists: []string{a}})

		require.NoError(t, f.repo.UpdateEventListedVenueName(f.ctx, id, "Zepp DiverCity"))
		got := f.read(id)
		assert.Equal(t, new("Zepp DiverCity"), got.listed)
		assert.Equal(t, []string{"Zepp"}, got.others)
	})

	t.Run("Unknown event", func(t *testing.T) {
		// @spec components/entity/concert/update-event-listed-venue-name "Unknown event"
		f := newVisibilityFixture(t, db)
		require.NoError(t, f.repo.UpdateEventListedVenueName(f.ctx, entity.NewID(), "Zepp"))
	})
}

func TestConcertRepository_AddOtherListedVenueName(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("New name", func(t *testing.T) {
		// @spec components/entity/concert/add-other-listed-venue-name "New name"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-27", nil, nil)
		id := f.store(slotEvent{series: s, venue: v, date: slotDate, start: "17:00", listed: "京セラドーム大阪", artists: []string{a}})

		require.NoError(t, f.repo.AddOtherListedVenueName(f.ctx, id, "Kyocera Dome Osaka"))
		got := f.read(id)
		assert.Equal(t, new("京セラドーム大阪"), got.listed)
		assert.Equal(t, []string{"Kyocera Dome Osaka"}, got.others)
	})

	t.Run("Name already known", func(t *testing.T) {
		// @spec components/entity/concert/add-other-listed-venue-name "Name already known"
		f := newVisibilityFixture(t, db)
		a, s, v := f.artist(), f.discoveredSeries(), f.venue("JP-27", nil, nil)
		id := f.store(slotEvent{series: s, venue: v, date: slotDate, listed: "京セラドーム大阪", others: []string{"Kyocera Dome Osaka"}, artists: []string{a}})

		require.NoError(t, f.repo.AddOtherListedVenueName(f.ctx, id, "KYOCERA DOME OSAKA"))
		assert.Equal(t, []string{"Kyocera Dome Osaka"}, f.read(id).others)
		require.NoError(t, f.repo.AddOtherListedVenueName(f.ctx, id, "京セラドーム大阪"))
		assert.Equal(t, []string{"Kyocera Dome Osaka"}, f.read(id).others, "the listed venue name itself is not added")
	})

	t.Run("Unknown event", func(t *testing.T) {
		// @spec components/entity/concert/add-other-listed-venue-name "Unknown event"
		f := newVisibilityFixture(t, db)
		err := f.repo.AddOtherListedVenueName(f.ctx, entity.NewID(), "Kyocera Dome Osaka")
		require.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestConcertRepository_Merge(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// pair stores Artist A's unknown-start event and Artist B's 18:00 event
	// at one venue and date, each in a Series of its own.
	pair := func(f *visibilityFixture) (source, target, sourceSeries, a, b string) {
		a, b = f.artist(), f.artist()
		sourceSeries = f.discoveredSeries()
		v := f.venue("JP-13", nil, nil)
		source = f.store(slotEvent{series: sourceSeries, venue: v, date: slotDate, listed: "Zepp Haneda (TOKYO)", others: []string{"羽田"}, artists: []string{a}})
		target = f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: slotDate, start: "18:00", listed: "Zepp Haneda(TOKYO)", artists: []string{b}})
		return source, target, sourceSeries, a, b
	}

	t.Run("Unknown-start event joins a shared bill", func(t *testing.T) {
		// @spec components/entity/concert/merge "Unknown-start event joins a shared bill"
		f := newVisibilityFixture(t, db)
		source, target, _, a, b := pair(f)
		outboxBefore := f.count(`SELECT COUNT(*) FROM watermill_events_outbox`)

		require.NoError(t, f.repo.Merge(f.ctx, source, target))

		got := f.read(target)
		assert.ElementsMatch(t, []string{a, b}, got.performers)
		assert.Equal(t, new("18:00"), got.start)
		assert.Equal(t, []string{"羽田"}, got.others, "a name the target is already listed under is not added")
		assert.False(t, f.read(source).exists, "the event without a start time is gone")
		assert.Equal(t, outboxBefore, f.count(`SELECT COUNT(*) FROM watermill_events_outbox`), "nothing is announced")
	})

	t.Run("a source under the target's name and no other names", func(t *testing.T) {
		f := newVisibilityFixture(t, db)
		a, b, v := f.artist(), f.artist(), f.venue("JP-13", nil, nil)
		source := f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: slotDate, listed: "Zepp Haneda", artists: []string{a}})
		target := f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: slotDate, start: "18:00", listed: "Zepp Haneda", artists: []string{b}})

		require.NoError(t, f.repo.Merge(f.ctx, source, target))
		got := f.read(target)
		assert.Empty(t, got.others)
		assert.ElementsMatch(t, []string{a, b}, got.performers)
	})

	t.Run("Series kept", func(t *testing.T) {
		// @spec components/entity/concert/merge "Series kept"
		f := newVisibilityFixture(t, db)
		source, target, sourceSeries, _, _ := pair(f)

		require.NoError(t, f.repo.Merge(f.ctx, source, target))
		assert.Equal(t, 1, f.count(`SELECT COUNT(*) FROM series WHERE id = $1`, sourceSeries))
	})

	t.Run("Fan tracking the merged event", func(t *testing.T) {
		// @spec components/entity/concert/merge "Fan tracking the merged event"
		f := newVisibilityFixture(t, db)
		source, target, _, _, _ := pair(f)
		fan := f.fan()
		f.exec(`INSERT INTO ticket_journeys (user_id, event_id, status) VALUES ($1, $2, $3)`, fan, source, int(entity.TicketJourneyStatusTracking))

		require.NoError(t, f.repo.Merge(f.ctx, source, target))
		assert.Equal(t, int(entity.TicketJourneyStatusTracking),
			f.count(`SELECT status FROM ticket_journeys WHERE user_id = $1 AND event_id = $2`, fan, target))
	})

	t.Run("Fan tracking both events", func(t *testing.T) {
		// @spec components/entity/concert/merge "Fan tracking both events"
		f := newVisibilityFixture(t, db)
		source, target, _, _, _ := pair(f)
		fan := f.fan()
		f.exec(`INSERT INTO ticket_journeys (user_id, event_id, status) VALUES ($1, $2, $3)`, fan, target, int(entity.TicketJourneyStatusApplied))
		f.exec(`INSERT INTO ticket_journeys (user_id, event_id, status) VALUES ($1, $2, $3)`, fan, source, int(entity.TicketJourneyStatusTracking))

		require.NoError(t, f.repo.Merge(f.ctx, source, target))
		assert.Equal(t, int(entity.TicketJourneyStatusApplied),
			f.count(`SELECT status FROM ticket_journeys WHERE user_id = $1 AND event_id = $2`, fan, target))
		assert.Equal(t, 1, f.count(`SELECT COUNT(*) FROM ticket_journeys WHERE user_id = $1`, fan))
	})

	t.Run("Different date", func(t *testing.T) {
		// @spec components/entity/concert/merge "Different date"
		f := newVisibilityFixture(t, db)
		a, b, v := f.artist(), f.artist(), f.venue("JP-13", nil, nil)
		source := f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: slotDate, artists: []string{a}})
		target := f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: nextDate, start: "18:00", artists: []string{b}})

		err := f.repo.Merge(f.ctx, source, target)
		require.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.True(t, f.read(source).exists)
		assert.Equal(t, []string{b}, f.read(target).performers)
	})

	t.Run("First-party source", func(t *testing.T) {
		// @spec components/entity/concert/merge "First-party source"
		f := newVisibilityFixture(t, db)
		a, b, v := f.artist(), f.artist(), f.venue("JP-13", nil, nil)
		source := f.store(slotEvent{series: f.firstPartySeries(entity.SeriesVisibilityPublic), venue: v, date: slotDate, state: "PUBLISHED", artists: []string{a}})
		target := f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: slotDate, start: "18:00", artists: []string{b}})

		err := f.repo.Merge(f.ctx, source, target)
		require.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.True(t, f.read(source).exists)
		assert.Equal(t, []string{b}, f.read(target).performers)
	})

	t.Run("First-party target", func(t *testing.T) {
		// @spec components/entity/concert/merge "First-party target"
		f := newVisibilityFixture(t, db)
		a, b, v := f.artist(), f.artist(), f.venue("JP-13", nil, nil)
		source := f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: slotDate, artists: []string{a}})
		target := f.store(slotEvent{series: f.firstPartySeries(entity.SeriesVisibilityPublic), venue: v, date: slotDate, start: "18:00", state: "CANCELLED", artists: []string{b}})

		err := f.repo.Merge(f.ctx, source, target)
		require.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.True(t, f.read(source).exists)
		assert.Equal(t, []string{b}, f.read(target).performers)
	})

	t.Run("Last event of a series with a ticket sale", func(t *testing.T) {
		// @spec components/entity/concert/merge "Last event of a series with a ticket sale"
		f := newVisibilityFixture(t, db)
		source, target, sourceSeries, _, b := pair(f)
		f.exec(`INSERT INTO ticket_sales (id, series_id, name, method, time_zone, local_start_time, local_end_time, find_time)
			VALUES ($1, $2, 'FC先行', 2, 'Asia/Tokyo', '2026-12-01 12:00', '2026-12-05 23:59', now())`, entity.NewID(), sourceSeries)

		err := f.repo.Merge(f.ctx, source, target)
		require.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.True(t, f.read(source).exists, "nothing changes")
		assert.Equal(t, []string{b}, f.read(target).performers)
	})

	t.Run("a series with a ticket sale and another event is merged away from", func(t *testing.T) {
		f := newVisibilityFixture(t, db)
		source, target, sourceSeries, a, b := pair(f)
		f.exec(`INSERT INTO ticket_sales (id, series_id, name, method, time_zone, local_start_time, local_end_time, find_time)
			VALUES ($1, $2, 'FC先行', 2, 'Asia/Tokyo', '2026-12-01 12:00', '2026-12-05 23:59', now())`, entity.NewID(), sourceSeries)
		f.store(slotEvent{series: sourceSeries, venue: f.venue("JP-27", nil, nil), date: nextDate, artists: []string{a}})

		require.NoError(t, f.repo.Merge(f.ctx, source, target))
		assert.ElementsMatch(t, []string{a, b}, f.read(target).performers)
	})

	t.Run("an event is not merged into itself", func(t *testing.T) {
		f := newVisibilityFixture(t, db)
		source, _, _, _, _ := pair(f)
		require.ErrorIs(t, f.repo.Merge(f.ctx, source, source), apperr.ErrInvalidArgument)
	})

	t.Run("an unknown event is not found", func(t *testing.T) {
		f := newVisibilityFixture(t, db)
		source, _, _, _, _ := pair(f)
		require.ErrorIs(t, f.repo.Merge(f.ctx, source, entity.NewID()), apperr.ErrNotFound)
		assert.True(t, f.read(source).exists)
	})
}

// TestConcertRepository_MergeRefusesAJourneyWrittenDuringTheMerge checks that
// a ticket journey written for the source while Merge holds the source's lock
// fails instead of being removed with the source. The target is locked by
// another transaction first, so Merge, which locks in id order, holds the
// source (the smaller id) and waits for the target; the journey is inserted
// then, and the target is released.
func TestConcertRepository_MergeRefusesAJourneyWrittenDuringTheMerge(t *testing.T) {
	t.Parallel()
	db := newTestDBWithPool(t, 6)
	f := newVisibilityFixture(t, db)
	a, b, v := f.artist(), f.artist(), f.venue("JP-13", nil, nil)
	source := f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: slotDate, artists: []string{a}})
	target := f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: slotDate, start: "18:00", artists: []string{b}})
	require.Less(t, source, target, "the source is locked first")
	fan := f.fan()
	ctx := context.Background()

	holder, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Rollback(ctx) }()
	_, err = holder.Exec(ctx, `SELECT 1 FROM events WHERE id = $1 FOR UPDATE`, target)
	require.NoError(t, err)

	// waiting reports how many sessions wait for a lock on a row of events.
	waiting := func() int {
		return f.count(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`)
	}

	merged := make(chan error, 1)
	go func() { merged <- f.repo.Merge(ctx, source, target) }()
	require.Eventually(t, func() bool { return waiting() == 1 }, 10*time.Second, 20*time.Millisecond, "Merge waits for the target")

	journey := make(chan error, 1)
	go func() {
		_, err := db.Pool.Exec(ctx, `INSERT INTO ticket_journeys (user_id, event_id, status) VALUES ($1, $2, 1)`, fan, source)
		journey <- err
	}()
	require.Eventually(t, func() bool { return waiting() == 2 }, 10*time.Second, 20*time.Millisecond, "the journey waits for the source")

	require.NoError(t, holder.Rollback(ctx))
	require.NoError(t, <-merged)
	require.ErrorContains(t, <-journey, "23503", "the journey fails its foreign key check")
	assert.Equal(t, 0, f.count(`SELECT COUNT(*) FROM ticket_journeys WHERE user_id = $1`, fan))
	assert.False(t, f.read(source).exists)
}

func TestSeriesRepository_ListUpcomingToursByArtist(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	tour := func(f *visibilityFixture, title, typ string) string {
		id := entity.NewID()
		f.exec(`INSERT INTO series (id, title, type) VALUES ($1, $2, $3)`, id, title, typ)
		return id
	}
	now := time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC)
	before := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)

	t.Run("Tour with an upcoming date", func(t *testing.T) {
		// @spec components/entity/series/list-upcoming-tours-by-artist "Tour with an upcoming date"
		f := newVisibilityFixture(t, db)
		a, v := f.artist(), f.venue("JP-13", nil, nil)
		s := tour(f, "PUREFLOW", "TOUR")
		f.store(slotEvent{series: s, venue: v, date: before, artists: []string{a}})
		f.store(slotEvent{series: s, venue: v, date: slotDate, artists: []string{a}})
		f.store(slotEvent{series: s, venue: v, date: nextDate, artists: []string{a}})

		got, err := rdb.NewSeriesRepository(db, testOutbox).ListUpcomingToursByArtist(f.ctx, a, now)
		require.NoError(t, err)
		require.Len(t, got, 1, "the Series is returned once")
		assert.Equal(t, s, got[0].ID)
		assert.Equal(t, "PUREFLOW", got[0].Title)
	})

	t.Run("Tour already over", func(t *testing.T) {
		// @spec components/entity/series/list-upcoming-tours-by-artist "Tour already over"
		f := newVisibilityFixture(t, db)
		a, v := f.artist(), f.venue("JP-13", nil, nil)
		f.store(slotEvent{series: tour(f, "Over", "TOUR"), venue: v, date: before, artists: []string{a}})

		got, err := rdb.NewSeriesRepository(db, testOutbox).ListUpcomingToursByArtist(f.ctx, a, now)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("Standalone show", func(t *testing.T) {
		// @spec components/entity/series/list-upcoming-tours-by-artist "Standalone show"
		f := newVisibilityFixture(t, db)
		a, v := f.artist(), f.venue("JP-13", nil, nil)
		f.store(slotEvent{series: tour(f, "Live", "SINGLE"), venue: v, date: slotDate, artists: []string{a}})

		got, err := rdb.NewSeriesRepository(db, testOutbox).ListUpcomingToursByArtist(f.ctx, a, now)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("Organizer's tour", func(t *testing.T) {
		// @spec components/entity/series/list-upcoming-tours-by-artist "Organizer's tour"
		f := newVisibilityFixture(t, db)
		a, v := f.artist(), f.venue("JP-13", nil, nil)
		f.store(slotEvent{series: f.firstPartySeries(entity.SeriesVisibilityPublic), venue: v, date: slotDate, state: "PUBLISHED", artists: []string{a}})

		got, err := rdb.NewSeriesRepository(db, testOutbox).ListUpcomingToursByArtist(f.ctx, a, now)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("another artist's tour is not returned", func(t *testing.T) {
		f := newVisibilityFixture(t, db)
		a, other, v := f.artist(), f.artist(), f.venue("JP-13", nil, nil)
		f.store(slotEvent{series: tour(f, "Theirs", "TOUR"), venue: v, date: slotDate, artists: []string{other}})

		got, err := rdb.NewSeriesRepository(db, testOutbox).ListUpcomingToursByArtist(f.ctx, a, now)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

// TestConcertRepository_MergeWaitsForATicketSaleOfTheSeries checks that a
// discovered TicketSale inserted for the source's Series while Merge runs is
// seen by Merge's check: the insert's transaction holds the key share lock on
// the Series, Merge waits for it, and then refuses.
func TestConcertRepository_MergeWaitsForATicketSaleOfTheSeries(t *testing.T) {
	t.Parallel()
	db := newTestDBWithPool(t, 6)
	f := newVisibilityFixture(t, db)
	a, b, v := f.artist(), f.artist(), f.venue("JP-13", nil, nil)
	series := f.discoveredSeries()
	source := f.store(slotEvent{series: series, venue: v, date: slotDate, artists: []string{a}})
	target := f.store(slotEvent{series: f.discoveredSeries(), venue: v, date: slotDate, start: "18:00", artists: []string{b}})
	ctx := context.Background()

	sale, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = sale.Rollback(ctx) }()
	_, err = sale.Exec(ctx, `INSERT INTO ticket_sales (id, series_id, name, method, time_zone, local_start_time, local_end_time, find_time)
		VALUES ($1, $2, 'FC先行', 2, 'Asia/Tokyo', '2026-12-01 12:00', '2026-12-05 23:59', now())`, entity.NewID(), series)
	require.NoError(t, err)

	merged := make(chan error, 1)
	go func() { merged <- f.repo.Merge(ctx, source, target) }()
	require.Eventually(t, func() bool {
		return f.count(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) == 1
	}, 10*time.Second, 20*time.Millisecond, "Merge waits for the sale's transaction")

	require.NoError(t, sale.Commit(ctx))
	require.ErrorIs(t, <-merged, apperr.ErrFailedPrecondition)
	assert.True(t, f.read(source).exists)
}
