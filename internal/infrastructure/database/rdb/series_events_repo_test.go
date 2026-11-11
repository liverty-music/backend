package rdb_test

import (
	"context"
	"slices"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// authoredFixture holds one Organizer, one represented artist and one venue
// for the series lifecycle contract tests.
type authoredFixture struct {
	// db is the database of the test.
	db       *rdb.Database
	t        *testing.T
	ctx      context.Context
	repo     *rdb.SeriesRepository
	orgID    string
	artistID string
	venueID  string
}

var authoredFirstDate = time.Date(2027, 7, 1, 0, 0, 0, 0, time.UTC)

func newAuthoredFixture(t *testing.T, db *rdb.Database) *authoredFixture {
	t.Helper()
	cleanTables(t, db)
	return &authoredFixture{
		db:       db,
		t:        t,
		ctx:      context.Background(),
		repo:     rdb.NewSeriesRepository(db, testOutbox),
		orgID:    seedOrganizer(t, db),
		artistID: seedArtist(t, db, "Lifecycle Band", uuid.NewV4().String()),
		venueID:  seedVenue(t, db, "Lifecycle Hall"),
	}
}

// jstAt returns the instant of hour:00 JST on date.
func jstAt(date time.Time, hour int) *time.Time {
	t := date.Add(time.Duration(hour-9) * time.Hour)
	return &t
}

// series creates a first-party series with one event per given state, on
// consecutive dates from authoredFirstDate with no start time, and returns
// the series id and the event ids. Every event starts DRAFT through
// CreateDraft; the others are moved to their state directly.
func (f *authoredFixture) series(states ...entity.PublishState) (string, []string) {
	f.t.Helper()
	vis := entity.SeriesVisibilityPublic
	s := &entity.Series{ID: entity.NewID(), Title: "Lifecycle Tour", Type: entity.SeriesTypeTour, OrganizerID: &f.orgID, Visibility: &vis}
	events := make([]*entity.Event, len(states))
	ids := make([]string, len(states))
	for i := range states {
		events[i] = entity.NewAuthoredEvent(s.ID, f.venueID, new("Lifecycle Hall"), authoredFirstDate.AddDate(0, 0, i), nil, nil, nil)
		ids[i] = events[i].ID
	}
	require.NoError(f.t, f.repo.CreateDraft(f.ctx, s, events, []string{f.artistID}))
	for i, state := range states {
		if state != entity.PublishStateDraft {
			f.exec(`UPDATE events SET publish_state = $2::event_publish_state WHERE id = $1`, ids[i], string(state))
		}
	}
	return s.ID, ids
}

// otherSeries creates a first-party PUBLIC series of the given Organizer
// with one event in the given state at the venue on date, starting at start,
// and returns the event's id.
func (f *authoredFixture) otherSeries(date time.Time, start *time.Time, state entity.PublishState, organizerID string) string {
	f.t.Helper()
	seriesID := entity.NewID()
	f.exec(`INSERT INTO series (id, title, type, organizer_id, visibility) VALUES ($1, 'Other Tour', 'TOUR', $2, 'PUBLIC')`, seriesID, organizerID)
	eventID := entity.NewID()
	f.exec(`INSERT INTO events (id, series_id, venue_id, local_event_date, start_at, publish_state, per_account_limit) VALUES ($1, $2, $3, $4, $5, $6::event_publish_state, 4)`,
		eventID, seriesID, f.venueID, date, start, string(state))
	f.exec(`INSERT INTO concerts (event_id) VALUES ($1)`, eventID)
	return eventID
}

// discovered creates a discovered event at the venue on date, starting at
// start, tracked by one fan, and returns its id and the fan's id.
func (f *authoredFixture) discovered(date time.Time, start *time.Time) (eventID, fanID string) {
	f.t.Helper()
	seriesID := entity.NewID()
	f.exec(`INSERT INTO series (id, title, type) VALUES ($1, 'Found Tour', 'TOUR')`, seriesID)
	eventID = entity.NewID()
	f.exec(`INSERT INTO events (id, series_id, venue_id, local_event_date, start_at) VALUES ($1, $2, $3, $4, $5)`,
		eventID, seriesID, f.venueID, date, start)
	f.exec(`INSERT INTO concerts (event_id) VALUES ($1)`, eventID)
	fanID = seedUser(f.t, f.db, "Fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
	f.exec(`INSERT INTO ticket_journeys (user_id, event_id, status) VALUES ($1, $2, 1)`, fanID, eventID)
	return eventID, fanID
}

func (f *authoredFixture) suppress(date time.Time, start *time.Time) {
	f.t.Helper()
	f.exec(`INSERT INTO suppressed_concerts (id, venue_id, local_event_date, start_at) VALUES ($1, $2, $3, $4)`,
		entity.NewID(), f.venueID, date, start)
}

func (f *authoredFixture) exec(sql string, args ...any) {
	f.t.Helper()
	_, err := f.db.Pool.Exec(f.ctx, sql, args...)
	require.NoError(f.t, err, sql)
}

// state reads an event's publish state; ok is false when the event is gone.
func (f *authoredFixture) state(eventID string) (state string, ok bool) {
	f.t.Helper()
	var s *string
	err := f.db.Pool.QueryRow(f.ctx, `SELECT publish_state::text FROM events WHERE id = $1`, eventID).Scan(&s)
	if err != nil {
		return "", false
	}
	if s == nil {
		return "", true
	}
	return *s, true
}

// limit reads an event's per-account limit.
func (f *authoredFixture) limit(eventID string) int {
	f.t.Helper()
	var limit int
	require.NoError(f.t, f.db.Pool.QueryRow(f.ctx, `SELECT per_account_limit FROM events WHERE id = $1`, eventID).Scan(&limit))
	return limit
}

func (f *authoredFixture) seriesOf(eventID string) string {
	f.t.Helper()
	var seriesID string
	require.NoError(f.t, f.db.Pool.QueryRow(f.ctx, `SELECT series_id FROM events WHERE id = $1`, eventID).Scan(&seriesID))
	return seriesID
}

func (f *authoredFixture) performers(eventID string) []string {
	f.t.Helper()
	rows, err := f.db.Pool.Query(f.ctx, `SELECT artist_id FROM concert_artists WHERE event_id = $1 ORDER BY artist_id`, eventID)
	require.NoError(f.t, err)
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		require.NoError(f.t, rows.Scan(&id))
		ids = append(ids, id)
	}
	return ids
}

// edit returns the series as stored with its events as Update takes them.
func (f *authoredFixture) edit(seriesID string) (*entity.Series, []*entity.Event) {
	f.t.Helper()
	s, concerts, err := f.repo.GetAuthored(f.ctx, seriesID)
	events := entity.EventsOf(concerts)
	require.NoError(f.t, err)
	return s, events
}

func TestSeriesRepository_CreateDraft(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("store two DRAFT events, each a concert with the performer and its limit", func(t *testing.T) {
		// @spec components/entity/series/create-draft "Draft with two events"
		f := newAuthoredFixture(t, db)
		vis := entity.SeriesVisibilityPublic
		s := &entity.Series{ID: entity.NewID(), Title: "Lifecycle Tour", Type: entity.SeriesTypeTour, OrganizerID: &f.orgID, Visibility: &vis}
		limited := entity.NewAuthoredEvent(s.ID, f.venueID, nil, authoredFirstDate, nil, nil, new(2))
		unlimited := entity.NewAuthoredEvent(s.ID, f.venueID, nil, authoredFirstDate.AddDate(0, 0, 1), nil, nil, nil)

		require.NoError(t, f.repo.CreateDraft(f.ctx, s, []*entity.Event{limited, unlimited}, []string{f.artistID}))

		_, concerts, err := f.repo.GetAuthored(f.ctx, s.ID)
		require.NoError(t, err)
		require.Len(t, entity.EventsOf(concerts), 2)
		for _, id := range []string{limited.ID, unlimited.ID} {
			state, ok := f.state(id)
			require.True(t, ok)
			assert.Equal(t, "DRAFT", state)
			assert.Equal(t, []string{f.artistID}, f.performers(id))
		}
		assert.Equal(t, 2, f.limit(limited.ID))
		assert.Equal(t, entity.EventDefaultPerAccountLimit, f.limit(unlimited.ID))
	})

	t.Run("fail with INVALID_ARGUMENT and store nothing for a limit above one admission code", func(t *testing.T) {
		// @spec components/entity/series/create-draft "Limit above one admission code"
		f := newAuthoredFixture(t, db)
		vis := entity.SeriesVisibilityPublic
		s := &entity.Series{ID: entity.NewID(), Title: "Big Group", Type: entity.SeriesTypeSingle, OrganizerID: &f.orgID, Visibility: &vis}
		e := entity.NewAuthoredEvent(s.ID, f.venueID, nil, authoredFirstDate, nil, nil, new(11))

		err := f.repo.CreateDraft(f.ctx, s, []*entity.Event{e}, []string{f.artistID})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		_, err = f.repo.Get(f.ctx, s.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("fail with FAILED_PRECONDITION and store nothing for an unknown venue", func(t *testing.T) {
		// @spec components/entity/series/create-draft "Unknown venue"
		f := newAuthoredFixture(t, db)
		vis := entity.SeriesVisibilityPublic
		s := &entity.Series{ID: entity.NewID(), Title: "Lost", Type: entity.SeriesTypeSingle, OrganizerID: &f.orgID, Visibility: &vis}
		e := entity.NewAuthoredEvent(s.ID, entity.NewID(), nil, authoredFirstDate, nil, nil, nil)

		err := f.repo.CreateDraft(f.ctx, s, []*entity.Event{e}, []string{f.artistID})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = f.repo.Get(f.ctx, s.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestSeriesRepository_GetAuthored(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("read a draft series with both events DRAFT", func(t *testing.T) {
		// @spec components/entity/series/get-authored "Draft series read"
		f := newAuthoredFixture(t, db)
		seriesID, _ := f.series(entity.PublishStateDraft, entity.PublishStateDraft)

		s, concerts, err := f.repo.GetAuthored(f.ctx, seriesID)
		events := entity.EventsOf(concerts)
		artists := entity.Performers(concerts)

		require.NoError(t, err)
		require.NotNil(t, s.PublishState)
		assert.Equal(t, entity.PublishStateDraft, *s.PublishState)
		require.Len(t, events, 2)
		for _, e := range events {
			assert.True(t, e.HasPublishState(entity.PublishStateDraft))
		}
		require.Len(t, artists, 1)
	})

	t.Run("read every event of a published tour with its own state", func(t *testing.T) {
		// @spec components/entity/series/get-authored "Published tour with a new date"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished, entity.PublishStatePublished, entity.PublishStateDraft)

		s, concerts, err := f.repo.GetAuthored(f.ctx, seriesID)
		events := entity.EventsOf(concerts)

		require.NoError(t, err)
		assert.Equal(t, entity.PublishStatePublished, *s.PublishState)
		require.Len(t, events, 3)
		for i, want := range []entity.PublishState{entity.PublishStatePublished, entity.PublishStatePublished, entity.PublishStateDraft} {
			assert.Equal(t, ids[i], events[i].ID, "events in date order")
			assert.True(t, events[i].HasPublishState(want))
		}
	})

	t.Run("give each event its own performers", func(t *testing.T) {
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished, entity.PublishStateCancelled)
		guest := seedArtist(t, db, "Guest Act", uuid.NewV4().String())
		s, events := f.edit(seriesID)
		require.NoError(t, f.repo.Update(f.ctx, s, events, []string{f.artistID, guest}))

		_, concerts, err := f.repo.GetAuthored(f.ctx, seriesID)

		require.NoError(t, err)
		require.Len(t, concerts, 2)
		assert.Equal(t, ids[0], concerts[0].ID)
		assert.ElementsMatch(t, []string{f.artistID, guest}, concerts[0].ArtistIDs(), "the published date gains the guest")
		assert.Equal(t, []string{f.artistID}, concerts[1].ArtistIDs(), "the cancelled date keeps its performers")
		assert.ElementsMatch(t, []string{f.artistID, guest}, artistIDsOf(entity.Performers(concerts)))
	})

	t.Run("fail with NOT_FOUND for an unknown series id", func(t *testing.T) {
		// @spec components/entity/series/get-authored "Unknown series id"
		f := newAuthoredFixture(t, db)

		_, _, err := f.repo.GetAuthored(f.ctx, entity.NewID())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestSeriesRepository_Update(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("remove the DRAFT event that is not given", func(t *testing.T) {
		// @spec components/entity/series/update "Draft date removed"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft, entity.PublishStateDraft)
		s, events := f.edit(seriesID)

		require.NoError(t, f.repo.Update(f.ctx, s, events[:1], []string{f.artistID}))

		_, ok := f.state(ids[1])
		assert.False(t, ok)
		state, _ := f.state(ids[0])
		assert.Equal(t, "DRAFT", state)
	})

	t.Run("add an event without an id to a published tour as a DRAFT event", func(t *testing.T) {
		// @spec components/entity/series/update "Date added to a published tour"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished)
		s, events := f.edit(seriesID)
		added := &entity.Event{VenueID: f.venueID, LocalDate: authoredFirstDate.AddDate(0, 0, 7)}

		require.NoError(t, f.repo.Update(f.ctx, s, append(events, added), []string{f.artistID}))

		_, after := f.edit(seriesID)
		require.Len(t, after, 2)
		assert.Equal(t, ids[0], after[0].ID)
		assert.True(t, after[0].HasPublishState(entity.PublishStatePublished))
		assert.True(t, after[0].LocalDate.Equal(authoredFirstDate))
		assert.True(t, after[1].HasPublishState(entity.PublishStateDraft))
		assert.Equal(t, []string{f.artistID}, f.performers(after[1].ID))
		assert.Equal(t, entity.EventDefaultPerAccountLimit, f.limit(after[1].ID), "a new event without a limit gets the default")
	})

	t.Run("set a DRAFT event's per-account limit", func(t *testing.T) {
		// @spec components/entity/series/update "Draft limit changed"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		require.Equal(t, 4, f.limit(ids[0]))
		s, events := f.edit(seriesID)
		events[0].PerAccountLimit = new(2)

		require.NoError(t, f.repo.Update(f.ctx, s, events, []string{f.artistID}))

		assert.Equal(t, 2, f.limit(ids[0]))
	})

	t.Run("keep a PUBLISHED event's per-account limit", func(t *testing.T) {
		// @spec components/entity/series/update "Published limit kept"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished)
		s, events := f.edit(seriesID)
		events[0].PerAccountLimit = new(2)

		require.NoError(t, f.repo.Update(f.ctx, s, events, []string{f.artistID}))

		assert.Equal(t, 4, f.limit(ids[0]))
	})

	t.Run("fail with INVALID_ARGUMENT and change nothing for a DRAFT limit of 11", func(t *testing.T) {
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		s, events := f.edit(seriesID)
		events[0].PerAccountLimit = new(11)
		s.Title = "Changed"

		err := f.repo.Update(f.ctx, s, events, []string{f.artistID})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Equal(t, 4, f.limit(ids[0]))
		stored, _ := f.edit(seriesID)
		assert.Equal(t, "Lifecycle Tour", stored.Title)
	})

	t.Run("change a PUBLISHED event's open time and keep its venue and date", func(t *testing.T) {
		// @spec components/entity/series/update "Doors-open time corrected"
		f := newAuthoredFixture(t, db)
		seriesID, _ := f.series(entity.PublishStatePublished)
		s, events := f.edit(seriesID)
		events[0].OpenTime = jstAt(authoredFirstDate, 17)
		events[0].VenueID = seedVenue(t, db, "Elsewhere")
		events[0].LocalDate = authoredFirstDate.AddDate(0, 0, 3)

		require.NoError(t, f.repo.Update(f.ctx, s, events, []string{f.artistID}))

		_, after := f.edit(seriesID)
		assert.True(t, after[0].OpenTime.Equal(*jstAt(authoredFirstDate, 17)))
		assert.Equal(t, f.venueID, after[0].VenueID)
		assert.True(t, after[0].LocalDate.Equal(authoredFirstDate))
	})

	t.Run("add a guest performer to a PUBLISHED event", func(t *testing.T) {
		// @spec components/entity/series/update "Guest performer added after publish"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished)
		guest := seedArtist(t, db, "Guest Act", uuid.NewV4().String())
		s, events := f.edit(seriesID)

		require.NoError(t, f.repo.Update(f.ctx, s, events, []string{f.artistID, guest}))

		assert.ElementsMatch(t, []string{f.artistID, guest}, f.performers(ids[0]))
		_, after := f.edit(seriesID)
		assert.Equal(t, f.venueID, after[0].VenueID)
	})

	t.Run("fail with FAILED_PRECONDITION for an event of another series", func(t *testing.T) {
		// @spec components/entity/series/update "Event of another series"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		_, otherIDs := f.series(entity.PublishStateDraft)
		s, events := f.edit(seriesID)
		foreign := *events[0]
		foreign.ID = otherIDs[0]
		s.Title = "Changed"

		err := f.repo.Update(f.ctx, s, []*entity.Event{&foreign}, []string{f.artistID})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		stored, _ := f.edit(seriesID)
		assert.Equal(t, "Lifecycle Tour", stored.Title)
		_, ok := f.state(ids[0])
		assert.True(t, ok)
	})

	t.Run("set the first start time of a PUBLISHED event and keep its id", func(t *testing.T) {
		// @spec components/entity/series/update "Start time announced"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished)
		s, events := f.edit(seriesID)
		events[0].StartTime = jstAt(authoredFirstDate, 18)

		require.NoError(t, f.repo.Update(f.ctx, s, events, []string{f.artistID}))

		_, after := f.edit(seriesID)
		assert.Equal(t, ids[0], after[0].ID)
		assert.True(t, after[0].StartTime.Equal(*jstAt(authoredFirstDate, 18)))
	})

	t.Run("merge a discovered event at the new slot into the PUBLISHED event", func(t *testing.T) {
		// @spec components/entity/series/update "Discovered event at the new slot"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished)
		discoveredID, fanID := f.discovered(authoredFirstDate, jstAt(authoredFirstDate, 18))
		s, events := f.edit(seriesID)
		events[0].StartTime = jstAt(authoredFirstDate, 18)

		require.NoError(t, f.repo.Update(f.ctx, s, events, []string{f.artistID}))

		_, ok := f.state(discoveredID)
		assert.False(t, ok, "the discovered event is gone")
		var tracked string
		require.NoError(t, db.Pool.QueryRow(f.ctx, `SELECT event_id FROM ticket_journeys WHERE user_id = $1`, fanID).Scan(&tracked))
		assert.Equal(t, ids[0], tracked, "the fan tracks the published event")
		_, after := f.edit(seriesID)
		assert.True(t, after[0].StartTime.Equal(*jstAt(authoredFirstDate, 18)))
	})

	t.Run("take a suppressed new slot", func(t *testing.T) {
		// @spec components/entity/series/update "Suppressed new slot"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished)
		f.suppress(authoredFirstDate, jstAt(authoredFirstDate, 18))
		s, events := f.edit(seriesID)
		events[0].StartTime = jstAt(authoredFirstDate, 18)

		require.NoError(t, f.repo.Update(f.ctx, s, events, []string{f.artistID}))

		_, after := f.edit(seriesID)
		assert.Equal(t, ids[0], after[0].ID)
		require.NotNil(t, after[0].StartTime)
		assert.True(t, after[0].StartTime.Equal(*jstAt(authoredFirstDate, 18)))
	})

	t.Run("fail with FAILED_PRECONDITION when an event of another first-party series holds the new slot", func(t *testing.T) {
		// @spec components/entity/series/update "First-party event at the new slot"
		f := newAuthoredFixture(t, db)
		seriesID, _ := f.series(entity.PublishStatePublished)
		f.otherSeries(authoredFirstDate, jstAt(authoredFirstDate, 18), entity.PublishStatePublished, seedOrganizer(t, db))
		s, events := f.edit(seriesID)
		events[0].StartTime = jstAt(authoredFirstDate, 18)

		err := f.repo.Update(f.ctx, s, events, []string{f.artistID})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, after := f.edit(seriesID)
		assert.Nil(t, after[0].StartTime)
	})
}

func TestSeriesRepository_CreateDraft_IDAlreadyTaken(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec components/entity/series/create-draft "Id already taken"
	f := newAuthoredFixture(t, db)
	vis := entity.SeriesVisibilityPublic
	first := &entity.Series{ID: entity.NewID(), Title: "First Tour", Type: entity.SeriesTypeTour, OrganizerID: &f.orgID, Visibility: &vis}
	firstEvent := entity.NewAuthoredEvent(first.ID, f.venueID, nil, authoredFirstDate, nil, nil, nil)
	require.NoError(t, f.repo.CreateDraft(f.ctx, first, []*entity.Event{firstEvent}, []string{f.artistID}))

	// The same id again, with another title and other Events.
	again := &entity.Series{ID: first.ID, Title: "Second Tour", Type: entity.SeriesTypeTour, OrganizerID: &f.orgID, Visibility: &vis}
	other := entity.NewAuthoredEvent(first.ID, f.venueID, nil, authoredFirstDate.AddDate(0, 0, 7), nil, nil, nil)
	err := f.repo.CreateDraft(f.ctx, again, []*entity.Event{other}, []string{f.artistID})

	assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	stored, concerts, err := f.repo.GetAuthored(f.ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "First Tour", stored.Title)
	assert.Equal(t, []string{firstEvent.ID}, eventIDs(entity.EventsOf(concerts)), "the existing Series keeps exactly the Events it had")
	_, ok := f.state(other.ID)
	assert.False(t, ok, "the given Event is not stored")
}

func TestSeriesRepository_PublishEvents(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("publish a draft at a free slot and return its id", func(t *testing.T) {
		// @spec components/entity/series/publish-events "New slot"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)

		got, err := f.repo.PublishEvents(f.ctx, seriesID, ids, nil)

		require.NoError(t, err)
		assert.Equal(t, ids, got)
		state, _ := f.state(ids[0])
		assert.Equal(t, "PUBLISHED", state)
	})

	t.Run("claim a discovered event at the draft's slot", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Claim a discovered event"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		discoveredID, _ := f.discovered(authoredFirstDate, nil)

		got, err := f.repo.PublishEvents(f.ctx, seriesID, ids, nil)

		require.NoError(t, err)
		assert.Empty(t, got, "a claimed event is not returned")
		state, ok := f.state(discoveredID)
		require.True(t, ok)
		assert.Equal(t, "PUBLISHED", state)
		assert.Equal(t, seriesID, f.seriesOf(discoveredID))
		assert.Equal(t, []string{f.artistID}, f.performers(discoveredID))
		_, ok = f.state(ids[0])
		assert.False(t, ok, "the draft is gone")
	})

	t.Run("publish a draft starting at 18:00 next to an unknown-start event", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Known start next to unknown-start event"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		f.discovered(authoredFirstDate, nil)
		s, events := f.edit(seriesID)
		events[0].StartTime = jstAt(authoredFirstDate, 18)
		require.NoError(t, f.repo.Update(f.ctx, s, events, []string{f.artistID}))

		got, err := f.repo.PublishEvents(f.ctx, seriesID, ids, nil)

		require.NoError(t, err)
		assert.Equal(t, ids, got)
	})

	t.Run("leave the draft that is not given as a draft", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Other drafts stay drafts"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft, entity.PublishStateDraft)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids[:1], nil)

		require.NoError(t, err)
		first, _ := f.state(ids[0])
		second, _ := f.state(ids[1])
		assert.Equal(t, "PUBLISHED", first)
		assert.Equal(t, "DRAFT", second)
	})

	blocked := []struct {
		name  string
		block func(f *authoredFixture)
	}{
		{
			// @spec components/entity/series/publish-events "Another Organizer's slot"
			name: "another organizer's event at the slot",
			block: func(f *authoredFixture) {
				f.otherSeries(authoredFirstDate.AddDate(0, 0, 1), nil, entity.PublishStatePublished, seedOrganizer(f.t, db))
			},
		},
		{
			// @spec components/entity/series/publish-events "Cancelled slot"
			name: "a CANCELLED event at the slot",
			block: func(f *authoredFixture) {
				f.otherSeries(authoredFirstDate.AddDate(0, 0, 1), nil, entity.PublishStateCancelled, f.orgID)
			},
		},
	}
	for _, tt := range blocked {
		t.Run("fail with FAILED_PRECONDITION and keep every event DRAFT for "+tt.name, func(t *testing.T) {
			f := newAuthoredFixture(t, db)
			seriesID, ids := f.series(entity.PublishStateDraft, entity.PublishStateDraft)
			tt.block(f)

			_, err := f.repo.PublishEvents(f.ctx, seriesID, ids, nil)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			for _, id := range ids {
				state, _ := f.state(id)
				assert.Equal(t, "DRAFT", state)
			}
		})
	}

	t.Run("fail with FAILED_PRECONDITION and keep the held event in its series for another series of the same organizer at the slot", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Slot of another Series of the same Organizer"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		heldID := f.otherSeries(authoredFirstDate, nil, entity.PublishStatePublished, f.orgID)
		heldSeriesID := f.seriesOf(heldID)
		f.exec(`UPDATE series SET visibility = 'UNLISTED' WHERE id = $1`, heldSeriesID)
		stock := seedStockForEvent(t, db, heldID, entity.TicketSaleMethodFirstCome, 100, 4)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		state, _ := f.state(ids[0])
		assert.Equal(t, "DRAFT", state)
		assert.Equal(t, heldSeriesID, f.seriesOf(heldID))
		var visibility, stockSeriesID, saleSeriesID string
		require.NoError(t, db.Pool.QueryRow(f.ctx, `SELECT visibility::text FROM series WHERE id = $1`, heldSeriesID).Scan(&visibility))
		require.NoError(t, db.Pool.QueryRow(f.ctx, `
			SELECT st.series_id FROM ticket_stocks ts JOIN seat_types st ON st.id = ts.seat_type_id
			WHERE ts.id = $1 AND ts.event_id = $2`, string(stock.ID), heldID).Scan(&stockSeriesID))
		require.NoError(t, db.Pool.QueryRow(f.ctx, `
			SELECT s.series_id FROM ticket_sales s JOIN ticket_sale_events se ON se.ticket_sale_id = s.id
			WHERE s.id = $1 AND se.event_id = $2`, string(stock.SaleID), heldID).Scan(&saleSeriesID))
		assert.Equal(t, "UNLISTED", visibility)
		assert.Equal(t, heldSeriesID, stockSeriesID)
		assert.Equal(t, heldSeriesID, saleSeriesID)
	})

	t.Run("publish a draft at a suppressed slot", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Suppressed slot"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		f.suppress(authoredFirstDate, nil)

		got, err := f.repo.PublishEvents(f.ctx, seriesID, ids, nil)

		require.NoError(t, err)
		assert.Equal(t, ids, got)
		state, _ := f.state(ids[0])
		assert.Equal(t, "PUBLISHED", state)
	})

	t.Run("fail with FAILED_PRECONDITION for an event already PUBLISHED", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Already published"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("fail with INVALID_ARGUMENT when no event is given", func(t *testing.T) {
		// @spec components/entity/series/publish-events "No event given"
		f := newAuthoredFixture(t, db)
		seriesID, _ := f.series(entity.PublishStateDraft)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, nil, nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

// TestSeriesRepository_PublishEvents_ShareToken covers the share token that
// PublishEvents stores together with the publish.
func TestSeriesRepository_PublishEvents_ShareToken(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// unlisted makes the fixture's series UNLISTED, with the given share
	// token when one is given.
	unlisted := func(f *authoredFixture, seriesID string, token *string) {
		f.exec(`UPDATE series SET visibility = 'UNLISTED', unlisted_token = $2 WHERE id = $1`, seriesID, token)
	}
	tokenOf := func(f *authoredFixture, seriesID string) *string {
		f.t.Helper()
		var token *string
		require.NoError(f.t, db.Pool.QueryRow(f.ctx, `SELECT unlisted_token FROM series WHERE id = $1`, seriesID).Scan(&token))
		return token
	}
	assertAllDraft := func(f *authoredFixture, ids []string) {
		f.t.Helper()
		for _, id := range ids {
			state, _ := f.state(id)
			assert.Equal(f.t, "DRAFT", state)
		}
	}

	t.Run("store the token with the first publish of an UNLISTED series", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Token stored with the first publish"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		unlisted(f, seriesID, nil)

		got, err := f.repo.PublishEvents(f.ctx, seriesID, ids, new("token-t"))

		require.NoError(t, err)
		assert.Equal(t, ids, got)
		state, _ := f.state(ids[0])
		assert.Equal(t, "PUBLISHED", state)
		opened, err := f.repo.GetByUnlistedToken(f.ctx, "token-t")
		require.NoError(t, err)
		assert.Equal(t, seriesID, opened.ID)
	})

	t.Run("fail with FAILED_PRECONDITION and store no token when a slot is held by a CANCELLED event", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Blocked publish stores no token"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft, entity.PublishStateDraft)
		unlisted(f, seriesID, nil)
		f.otherSeries(authoredFirstDate.AddDate(0, 0, 1), nil, entity.PublishStateCancelled, f.orgID)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids, new("token-t"))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assertAllDraft(f, ids)
		assert.Nil(t, tokenOf(f, seriesID))
	})

	t.Run("fail with FAILED_PRECONDITION and publish nothing when a token is given for a PUBLIC series", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Token for a public series"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids, new("token-t"))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assertAllDraft(f, ids)
		assert.Nil(t, tokenOf(f, seriesID))
	})

	t.Run("fail with FAILED_PRECONDITION and keep the first token when the series already has one", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Series already has a token"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		unlisted(f, seriesID, new("token-t1"))

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids, new("token-t2"))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assertAllDraft(f, ids)
		opened, err := f.repo.GetByUnlistedToken(f.ctx, "token-t1")
		require.NoError(t, err)
		assert.Equal(t, seriesID, opened.ID)
	})

	t.Run("fail with ALREADY_EXISTS and store no token when another series holds the token", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Token held by another series"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft)
		unlisted(f, seriesID, nil)
		holderEventID := f.otherSeries(authoredFirstDate.AddDate(0, 0, 7), nil, entity.PublishStatePublished, f.orgID)
		unlisted(f, f.seriesOf(holderEventID), new("token-held"))

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids, new("token-held"))

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		assertAllDraft(f, ids)
		assert.Nil(t, tokenOf(f, seriesID))
	})
}

func TestSeriesRepository_CancelEvents(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("cancel one PUBLISHED date of three and keep it", func(t *testing.T) {
		// @spec components/entity/series/cancel-events "One date of a tour cancelled"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished, entity.PublishStatePublished, entity.PublishStatePublished)

		got, err := f.repo.CancelEvents(f.ctx, seriesID, ids[1:2])

		require.NoError(t, err)
		assert.Equal(t, ids[1:2], got)
		for i, want := range []string{"PUBLISHED", "CANCELLED", "PUBLISHED"} {
			state, ok := f.state(ids[i])
			require.True(t, ok)
			assert.Equal(t, want, state)
		}
	})

	t.Run("remove an all-DRAFT series with its events", func(t *testing.T) {
		// @spec components/entity/series/cancel-events "Whole draft discarded"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateDraft, entity.PublishStateDraft)

		got, err := f.repo.CancelEvents(f.ctx, seriesID, ids)

		require.NoError(t, err)
		assert.Empty(t, got)
		_, err = f.repo.Get(f.ctx, seriesID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("cancel the PUBLISHED date and remove the DRAFT date given together", func(t *testing.T) {
		// @spec components/entity/series/cancel-events "Published and draft dates together"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished, entity.PublishStateDraft)

		got, err := f.repo.CancelEvents(f.ctx, seriesID, ids)

		require.NoError(t, err)
		assert.Equal(t, ids[:1], got)
		state, _ := f.state(ids[0])
		assert.Equal(t, "CANCELLED", state)
		_, ok := f.state(ids[1])
		assert.False(t, ok)
	})

	t.Run("fail with FAILED_PRECONDITION and change nothing for an event already CANCELLED", func(t *testing.T) {
		// @spec components/entity/series/cancel-events "Already cancelled"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStateCancelled, entity.PublishStatePublished)

		_, err := f.repo.CancelEvents(f.ctx, seriesID, ids)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		state, _ := f.state(ids[1])
		assert.Equal(t, "PUBLISHED", state)
	})

	t.Run("fail with NOT_FOUND for an unknown series", func(t *testing.T) {
		// @spec components/entity/series/cancel-events "Unknown series"
		f := newAuthoredFixture(t, db)

		_, err := f.repo.CancelEvents(f.ctx, entity.NewID(), []string{entity.NewID()})

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// cancelledOf returns the EVENT.cancelled announcements of the events
// recorded after mark.
func cancelledOf(t *testing.T, db *rdb.Database, mark int64, eventIDs ...string) []*entityv1.Event {
	t.Helper()
	return announcedSince(t, db, mark, entity.SubjectEventCancelled, func() *entityv1.Event { return &entityv1.Event{} },
		func(e *entityv1.Event) bool { return slices.Contains(eventIDs, e.GetId().GetValue()) })
}

func TestSeriesRepository_CancelEvents_Announces(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("each cancelled event announced once", func(t *testing.T) {
		// @spec components/entity/series/cancel-events "Two dates cancelled"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished, entity.PublishStatePublished)
		mark := outboxMark(t, db)

		_, err := f.repo.CancelEvents(f.ctx, seriesID, ids)

		require.NoError(t, err)
		got := cancelledOf(t, db, mark, ids...)
		require.Len(t, got, 2)
		announced := []string{got[0].GetId().GetValue(), got[1].GetId().GetValue()}
		assert.ElementsMatch(t, ids, announced)
	})

	t.Run("nothing announced for a removed draft", func(t *testing.T) {
		// @spec components/entity/series/cancel-events "Draft removed"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished, entity.PublishStateDraft)
		mark := outboxMark(t, db)

		_, err := f.repo.CancelEvents(f.ctx, seriesID, ids)

		require.NoError(t, err)
		assert.Len(t, cancelledOf(t, db, mark, ids[0]), 1)
		assert.Empty(t, cancelledOf(t, db, mark, ids[1]))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/series/cancel-events "Announcement cannot be recorded"
		f := newAuthoredFixture(t, db)
		seriesID, ids := f.series(entity.PublishStatePublished, entity.PublishStateDraft)

		_, err := rdb.NewSeriesRepository(db, failingOutbox{}).CancelEvents(f.ctx, seriesID, ids)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		state, ok := f.state(ids[0])
		require.True(t, ok)
		assert.Equal(t, "PUBLISHED", state)
		state, ok = f.state(ids[1])
		require.True(t, ok)
		assert.Equal(t, "DRAFT", state)
	})
}

func artistIDsOf(artists []*entity.Artist) []string {
	ids := make([]string, len(artists))
	for i, a := range artists {
		ids[i] = a.ID
	}
	return ids
}
