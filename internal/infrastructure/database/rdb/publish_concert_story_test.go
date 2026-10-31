package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// Story test for stories/publish-an-organizer-concert. It runs the real
// SeriesUseCase and ConcertUseCase over the real repositories and local
// Postgres, from an organizer's create, edit, publish and cancel to the fan
// list. The venue is already known by its place id, so the map catalog is
// never called; messaging is replaced by storyPublisher.

type publishStory struct {
	t         *testing.T
	ctx       context.Context
	series    usecase.SeriesUseCase
	concerts  usecase.ConcertUseCase
	publisher *storyPublisher
	orgID     string
	artistID  string
	placeID   string
	venueName string
	date      time.Time
}

func newPublishStory(t *testing.T) *publishStory {
	t.Helper()
	cleanDatabase(t)
	logger, err := logging.New()
	require.NoError(t, err)

	s := &publishStory{
		t:         t,
		ctx:       context.Background(),
		publisher: &storyPublisher{},
		orgID:     seedOrganizer(t),
		artistID:  seedArtist(t, "Story Band", uuid.NewV4().String()),
		placeID:   "ChIJ-story-" + uuid.NewV7().String(),
		venueName: "Zepp Haneda",
		date:      time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 30),
	}
	_, err = testDB.Pool.Exec(s.ctx, `INSERT INTO organizer_artists (organizer_id, artist_id) VALUES ($1, $2)`, s.orgID, s.artistID)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(s.ctx, `INSERT INTO venues (id, name, admin_area, google_place_id) VALUES ($1, 'Zepp Haneda (TOKYO)', 'JP-13', $2)`,
		entity.NewID(), s.placeID)
	require.NoError(t, err)

	seriesRepo := rdb.NewSeriesRepository(testDB, testOutbox)
	concertRepo := rdb.NewConcertRepository(testDB, testOutbox)
	venueRepo := rdb.NewVenueRepository(testDB)
	organizerRepo := rdb.NewOrganizerRepository(testDB, testOutbox)
	s.series = usecase.NewSeriesUseCase(seriesRepo, rdb.NewSeriesImageRepository(testDB), venueRepo, nil, organizerRepo,
		rdb.NewTicketSaleRepository(testDB, testOutbox), nil, usecase.StorageBuckets{}, s.publisher, logger, time.Now)
	s.concerts = usecase.NewConcertUseCase(nil, concertRepo, venueRepo, seriesRepo, organizerRepo,
		nil, nil, nil, nil, nil, s.publisher, nil, nil, 0, 0, logger)
	return s
}

// input returns the editor's input for a date day days after the story date;
// eventID is empty for a new date.
func (s *publishStory) input(eventID string, day int) *usecase.DraftEventInput {
	in := &usecase.DraftEventInput{VenueName: s.venueName, PlaceID: &s.placeID, LocalDate: s.date.AddDate(0, 0, day)}
	if eventID != "" {
		in.EventID = &eventID
	}
	return in
}

func (s *publishStory) draft(title string, visibility entity.SeriesVisibility) *entity.Series {
	return &entity.Series{Title: title, Type: entity.SeriesTypeTour, Visibility: &visibility}
}

// create creates a concert with one date and returns its series and event ids.
func (s *publishStory) create(visibility entity.SeriesVisibility) (string, string) {
	s.t.Helper()
	series, concerts, err := s.series.Create(s.ctx, s.orgID, s.draft("Story Tour", visibility),
		[]*usecase.DraftEventInput{s.input("", 0)}, []string{s.artistID})
	events := entity.EventsOf(concerts)
	require.NoError(s.t, err)
	require.Len(s.t, events, 1)
	return series.ID, events[0].ID
}

// listed returns the fan list of the performer.
func (s *publishStory) listed() []*entity.Concert {
	s.t.Helper()
	got, err := s.concerts.ListByArtist(s.ctx, s.artistID)
	require.NoError(s.t, err)
	return got
}

func TestStory_PublishAnOrganizerConcert(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	t.Run("a PUBLIC concert reaches the fan list only once published", func(t *testing.T) {
		// @spec stories/publish-an-organizer-concert "Public concert goes live"
		s := newPublishStory(t)
		seriesID, eventID := s.create(entity.SeriesVisibilityPublic)
		_, _, err := s.series.Update(s.ctx, s.orgID, seriesID, s.draft("Story Tour 2027", entity.SeriesVisibilityPublic),
			[]*usecase.DraftEventInput{s.input(eventID, 0)}, []string{s.artistID})
		require.NoError(t, err)
		assert.Empty(t, s.listed(), "a draft is on no fan list")

		mark := outboxMark(t)
		_, _, err = s.series.Publish(s.ctx, s.orgID, seriesID, nil)
		require.NoError(t, err)

		got := s.listed()
		require.Len(t, got, 1)
		assert.Equal(t, eventID, got[0].ID)
		assert.Equal(t, "Story Tour 2027", got[0].Series.Title)
		assert.Equal(t, [][]string{{eventID}}, concertsAnnouncedFor(t, mark, s.artistID), "its followers are told once")
	})

	t.Run("an UNLISTED concert stays off the fan list", func(t *testing.T) {
		// @spec stories/publish-an-organizer-concert "Unlisted concert stays off lists"
		s := newPublishStory(t)
		seriesID, _ := s.create(entity.SeriesVisibilityUnlisted)

		_, _, err := s.series.Publish(s.ctx, s.orgID, seriesID, nil)

		require.NoError(t, err)
		assert.Empty(t, s.listed())
	})

	t.Run("publishing claims the discovered concert at the slot with its id", func(t *testing.T) {
		// @spec stories/publish-an-organizer-concert "Discovered concert claimed"
		s := newPublishStory(t)
		seriesID, _ := s.create(entity.SeriesVisibilityPublic)
		found := entity.NewID()
		discoveredID := entity.NewID()
		_, err := testDB.Pool.Exec(s.ctx, `INSERT INTO series (id, title, type) VALUES ($1, 'Found Tour', 'TOUR')`, found)
		require.NoError(t, err)
		_, err = testDB.Pool.Exec(s.ctx, `INSERT INTO events (id, series_id, venue_id, local_event_date)
			SELECT $1, $2, id, $3 FROM venues WHERE google_place_id = $4`, discoveredID, found, s.date, s.placeID)
		require.NoError(t, err)
		_, err = testDB.Pool.Exec(s.ctx, `INSERT INTO concerts (event_id) VALUES ($1)`, discoveredID)
		require.NoError(t, err)

		mark := outboxMark(t)
		_, _, err = s.series.Publish(s.ctx, s.orgID, seriesID, nil)
		require.NoError(t, err)

		got := s.listed()
		require.Len(t, got, 1)
		assert.Equal(t, discoveredID, got[0].ID, "the discovered concert keeps its id")
		assert.Equal(t, seriesID, got[0].SeriesID, "and joins the organizer's series")
		assert.Empty(t, concertsAnnouncedFor(t, mark, s.artistID), "a claimed concert is not announced again")
	})

	t.Run("a cancelled concert leaves the fan list", func(t *testing.T) {
		// @spec stories/publish-an-organizer-concert "Cancel after publish"
		s := newPublishStory(t)
		seriesID, _ := s.create(entity.SeriesVisibilityPublic)
		_, _, err := s.series.Publish(s.ctx, s.orgID, seriesID, nil)
		require.NoError(t, err)
		require.Len(t, s.listed(), 1)

		require.NoError(t, s.series.Cancel(s.ctx, s.orgID, seriesID, nil))

		assert.Empty(t, s.listed())
	})

	t.Run("a tour adds a date that is listed and announced only once published", func(t *testing.T) {
		// @spec stories/publish-an-organizer-concert "Tour adds a date"
		s := newPublishStory(t)
		seriesID, firstID := s.create(entity.SeriesVisibilityPublic)
		_, _, err := s.series.Publish(s.ctx, s.orgID, seriesID, nil)
		require.NoError(t, err)

		_, concerts, err := s.series.Update(s.ctx, s.orgID, seriesID, s.draft("Story Tour", entity.SeriesVisibilityPublic),
			[]*usecase.DraftEventInput{s.input(firstID, 0), s.input("", 7)}, []string{s.artistID})
		events := entity.EventsOf(concerts)
		require.NoError(t, err)
		require.Len(t, events, 2)
		addedID := events[1].ID
		assert.Equal(t, []string{firstID}, concertIDs(s.listed()), "the new date is not listed before its publish")

		mark := outboxMark(t)
		_, _, err = s.series.Publish(s.ctx, s.orgID, seriesID, []string{addedID})
		require.NoError(t, err)

		assert.Equal(t, []string{firstID, addedID}, concertIDs(s.listed()))
		assert.Equal(t, [][]string{{addedID}}, concertsAnnouncedFor(t, mark, s.artistID))
	})

	t.Run("a doors-open time set after publish is listed", func(t *testing.T) {
		// @spec stories/publish-an-organizer-concert "Doors-open time announced after publish"
		s := newPublishStory(t)
		seriesID, eventID := s.create(entity.SeriesVisibilityPublic)
		_, _, err := s.series.Publish(s.ctx, s.orgID, seriesID, nil)
		require.NoError(t, err)
		in := s.input(eventID, 0)
		doors := s.date.Add(8 * time.Hour)
		in.OpenTime = &doors

		_, _, err = s.series.Update(s.ctx, s.orgID, seriesID, s.draft("Story Tour", entity.SeriesVisibilityPublic),
			[]*usecase.DraftEventInput{in}, []string{s.artistID})
		require.NoError(t, err)

		got := s.listed()
		require.Len(t, got, 1)
		require.NotNil(t, got[0].OpenTime)
		assert.True(t, got[0].OpenTime.Equal(doors))
	})

	t.Run("a published date cannot move to another venue", func(t *testing.T) {
		// @spec stories/publish-an-organizer-concert "Venue cannot move after publish"
		s := newPublishStory(t)
		seriesID, eventID := s.create(entity.SeriesVisibilityPublic)
		_, _, err := s.series.Publish(s.ctx, s.orgID, seriesID, nil)
		require.NoError(t, err)
		moved := s.input(eventID, 0)
		moved.VenueName, moved.PlaceID = "Budokan", new("ChIJ-budokan")

		_, _, err = s.series.Update(s.ctx, s.orgID, seriesID, s.draft("Story Tour", entity.SeriesVisibilityPublic),
			[]*usecase.DraftEventInput{moved}, []string{s.artistID})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		got := s.listed()
		require.Len(t, got, 1)
		assert.Equal(t, "Zepp Haneda (TOKYO)", got[0].Venue.Name)
	})
}
