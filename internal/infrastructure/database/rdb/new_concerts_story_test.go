package rdb_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"uuid"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Story test for stories/get-notified-of-new-concerts. Concerts are added by
// CreateFromDiscovered (the batch the daily search hands over; the search itself
// is not run) or by an Organizer's SeriesUseCase.Publish. Each CONCERT.created
// recorded in the outbox then goes to the consumer that handles it in
// production, whose NotifyNewConcerts starts the deliver-notification workflows,
// and those run Deliver over the real repositories. A fake sender stands in for
// the browser. The outbox forwarder and NATS are not run.

// newConcertsStory carries announced concerts through to the browsers.
type newConcertsStory struct {
	// db is the database of the test.
	db       *rdb.Database
	t        *testing.T
	ctx      context.Context
	d        *storyDeps
	consumer *event.NotificationConsumer
	mark     int64
}

func newNewConcertsStory(t *testing.T, db *rdb.Database) *newConcertsStory {
	t.Helper()
	d := newStoryDeps(t, db)
	return &newConcertsStory{
		db:       db,
		t:        t,
		ctx:      context.Background(),
		d:        d,
		consumer: event.NewNotificationConsumer(d.deliver, d.logger),
		mark:     outboxMark(t, db),
	}
}

// follow has the fan follow the artist at the hype level.
func (s *newConcertsStory) follow(fanID, artistID string, hype entity.Hype) {
	s.t.Helper()
	follows := rdb.NewFollowRepository(s.db, testOutbox)
	require.NoError(s.t, follows.Follow(s.ctx, fanID, artistID))
	require.NoError(s.t, follows.SetHype(s.ctx, fanID, artistID, hype))
}

// discover adds the given number of concerts of one tour for the artist through
// CreateFromDiscovered and returns their event ids, earliest first.
func (s *newConcertsStory) discover(artistID, artistName, tour string, count int) []string {
	s.t.Helper()
	places := knownPlaces{}
	batch := &entity.DiscoveredSeries{Title: tour, Type: entity.SeriesTypeTour, SourceURL: "https://example.com/" + uuid.NewV7().String()}
	first := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 60)
	for i := range count {
		name := fmt.Sprintf("Hall %d", i)
		places[name] = &entity.VenuePlace{
			ProviderUserID: "ChIJ-" + uuid.NewV7().String(),
			Name:           name,
			Coordinates:    &entity.Coordinates{Latitude: 35.55, Longitude: 139.75},
		}
		batch.Events = append(batch.Events, &entity.DiscoveredEvent{ListedVenueName: name, LocalDate: first.AddDate(0, 0, i)})
	}
	uc := usecase.NewConcertCreationUseCase(
		rdb.NewConcertCandidateRepository(s.db),
		rdb.NewVenueRepository(s.db),
		rdb.NewConcertRepository(s.db, testOutbox),
		rdb.NewSeriesRepository(s.db, testOutbox),
		rdb.NewSuppressedConcertRepository(s.db),
		places, nil, time.Now, s.d.logger,
	)
	require.NoError(s.t, uc.CreateFromDiscovered(s.ctx, entity.ConcertDiscoveredData{
		ArtistID: artistID, ArtistName: artistName, Series: []*entity.DiscoveredSeries{batch},
	}))

	rows, err := s.db.Pool.Query(s.ctx, `
		SELECT e.id::text FROM events e JOIN concert_artists ca ON ca.event_id = e.id
		WHERE ca.artist_id = $1 ORDER BY e.local_event_date`, artistID)
	require.NoError(s.t, err)
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		require.NoError(s.t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(s.t, rows.Err())
	require.Len(s.t, ids, count)
	return ids
}

// deliverAnnounced hands each CONCERT.created recorded since the last call to
// the consumer and runs the deliveries it starts.
func (s *newConcertsStory) deliverAnnounced() {
	s.t.Helper()
	for _, msg := range announcedMessages(s.t, s.db, s.mark, entity.SubjectConcertCreated) {
		require.NoError(s.t, s.consumer.Handle(msg))
	}
	s.mark = outboxMark(s.t, s.db)
	s.d.deliverRequested(s.t)
}

// newConcertsOf returns the fan's stored new_concerts Notifications.
func (s *newConcertsStory) newConcertsOf(fanID string) []*entity.Notification {
	s.t.Helper()
	return s.d.notificationList(s.t, fanID, entity.NotificationTypeNewConcerts)
}

func TestStory_GetNotifiedOfNewConcerts(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// @spec stories/get-notified-of-new-concerts "Away follower with one browser"
	t.Run("Away follower with one browser", func(t *testing.T) {
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Vaundy", uuid.NewV4().String())
		fan := storyFan(t, db, "away", "ja", "Asia/Tokyo")
		s := newNewConcertsStory(t, db)
		s.follow(fan, artistID, entity.HypeAway)

		ids := s.discover(artistID, "Vaundy", "ROADSIDE", 1)
		s.deliverAnnounced()

		pushes := s.d.push.to(fan)
		require.Len(t, pushes, 1)
		assert.Equal(t, "[Vaundy] 新着ライブ発表!!", pushes[0].Title)
		assert.Equal(t, "新しいライブが1件見つかりました\nROADSIDE", pushes[0].Body)
		assert.Equal(t, "/concerts/"+ids[0], pushes[0].Data[entity.NotificationDataKeyURL])
		records := s.newConcertsOf(fan)
		require.Len(t, records, 1)
		assert.Equal(t, entity.NotificationSubject{ArtistID: artistID, EventIDs: ids}, records[0].Subject)
	})

	// @spec stories/get-notified-of-new-concerts "Matched follower without a browser"
	t.Run("Matched follower without a browser", func(t *testing.T) {
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Vaundy", uuid.NewV4().String())
		fan := storyFanWithoutBrowser(t, db, "nobrowser", "ja", "Asia/Tokyo")
		s := newNewConcertsStory(t, db)
		s.follow(fan, artistID, entity.HypeAway)

		ids := s.discover(artistID, "Vaundy", "ROADSIDE", 1)
		s.deliverAnnounced()

		assert.Empty(t, s.d.push.to(fan))
		user, err := s.d.users.Get(s.ctx, fan)
		require.NoError(t, err)
		listed, err := s.d.deliver.List(s.ctx, user)
		require.NoError(t, err)
		require.Len(t, listed, 1, "the notification center lists it")
		assert.Equal(t, entity.NotificationTypeNewConcerts, listed[0].Notification.Type)
		assert.Equal(t, entity.NotificationSubject{ArtistID: artistID, EventIDs: ids}, listed[0].Notification.Subject)
		assert.Equal(t, "[Vaundy] 新着ライブ発表!!", listed[0].Message.Title)
	})

	// @spec stories/get-notified-of-new-concerts "Watch follower"
	t.Run("Watch follower", func(t *testing.T) {
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Vaundy", uuid.NewV4().String())
		watcher := storyFan(t, db, "watcher", "ja", "Asia/Tokyo")
		away := storyFan(t, db, "away", "ja", "Asia/Tokyo")
		s := newNewConcertsStory(t, db)
		s.follow(watcher, artistID, entity.HypeWatch)
		s.follow(away, artistID, entity.HypeAway)

		s.discover(artistID, "Vaundy", "ROADSIDE", 1)
		s.deliverAnnounced()

		assert.Empty(t, s.d.push.to(watcher))
		assert.Empty(t, s.newConcertsOf(watcher))
		assert.Len(t, s.d.push.to(away), 1, "the same concert reaches the Away follower")
	})

	// @spec stories/get-notified-of-new-concerts "Browser gone"
	t.Run("Browser gone", func(t *testing.T) {
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Vaundy", uuid.NewV4().String())
		fan := storyFan(t, db, "gone", "ja", "Asia/Tokyo")
		s := newNewConcertsStory(t, db)
		s.follow(fan, artistID, entity.HypeAway)
		s.d.push.goneEndpoints = map[string]bool{"https://push.example/gone": true}
		pushSubs := rdb.NewPushSubscriptionRepository(db, testOutbox)
		before, err := pushSubs.ListByUserIDs(s.ctx, []string{fan})
		require.NoError(t, err)
		require.Len(t, before, 1)

		s.discover(artistID, "Vaundy", "ROADSIDE", 1)
		s.deliverAnnounced()

		assert.Len(t, s.newConcertsOf(fan), 1, "the Notification is still recorded")
		after, err := pushSubs.ListByUserIDs(s.ctx, []string{fan})
		require.NoError(t, err)
		assert.Empty(t, after, "the browser's PushSubscription is removed")
	})

	// @spec stories/get-notified-of-new-concerts "Organizer publishes a co-headlined concert"
	t.Run("Organizer publishes a co-headlined concert", func(t *testing.T) {
		p := newPublishStory(t, db)
		second := seedArtist(t, db, "Second Band", uuid.NewV4().String())
		_, err := db.Pool.Exec(p.ctx, `INSERT INTO organizer_artists (organizer_id, artist_id) VALUES ($1, $2)`, p.orgID, second)
		require.NoError(t, err)
		fan := storyFan(t, db, "both", "ja", "Asia/Tokyo")
		s := newNewConcertsStory(t, db)
		s.follow(fan, p.artistID, entity.HypeAway)
		s.follow(fan, second, entity.HypeAway)

		series, concerts, err := p.series.Create(p.ctx, p.orgID, p.draft("Story Tour", entity.SeriesVisibilityPublic),
			[]*usecase.DraftEventInput{p.input("", 0)}, []string{p.artistID, second})
		require.NoError(t, err)
		events := entity.EventsOf(concerts)
		require.Len(t, events, 1)
		_, _, err = p.series.Publish(p.ctx, p.orgID, series.ID, nil)
		require.NoError(t, err)
		s.deliverAnnounced()

		pushes := s.d.push.to(fan)
		require.Len(t, pushes, 2)
		assert.ElementsMatch(t, []string{"[Story Band] 新着ライブ発表!!", "[Second Band] 新着ライブ発表!!"},
			[]string{pushes[0].Title, pushes[1].Title})
		for _, push := range pushes {
			assert.Equal(t, "新しいライブが1件見つかりました\nStory Tour", push.Body)
			assert.Equal(t, "/events/"+events[0].ID, push.Data[entity.NotificationDataKeyURL])
		}
		records := s.newConcertsOf(fan)
		require.Len(t, records, 2)
		assert.ElementsMatch(t, []string{p.artistID, second}, []string{records[0].Subject.ArtistID, records[1].Subject.ArtistID})
	})

	// @spec stories/get-notified-of-new-concerts "Unlisted concert"
	// Publishing is what decides this: it announces only the events of a PUBLIC
	// series. The PUBLIC concert published afterwards by the same Organizer shows
	// the path reaches this fan.
	t.Run("Unlisted concert", func(t *testing.T) {
		p := newPublishStory(t, db)
		fan := storyFan(t, db, "unlisted", "ja", "Asia/Tokyo")
		s := newNewConcertsStory(t, db)
		s.follow(fan, p.artistID, entity.HypeAway)

		unlistedSeries, unlistedEvent := p.create(entity.SeriesVisibilityUnlisted)
		_, _, err := p.series.Publish(p.ctx, p.orgID, unlistedSeries, nil)
		require.NoError(t, err)
		require.True(t, rowExists(t, db, `SELECT 1 FROM events WHERE id = $1 AND publish_state = 'PUBLISHED'`, unlistedEvent),
			"the unlisted concert is published")
		s.deliverAnnounced()

		assert.Empty(t, s.d.push.to(fan))
		assert.Empty(t, s.newConcertsOf(fan))

		publicSeries, concerts, err := p.series.Create(p.ctx, p.orgID, p.draft("Public Tour", entity.SeriesVisibilityPublic),
			[]*usecase.DraftEventInput{p.input("", 5)}, []string{p.artistID})
		require.NoError(t, err)
		publicEvents := entity.EventsOf(concerts)
		require.Len(t, publicEvents, 1)
		_, _, err = p.series.Publish(p.ctx, p.orgID, publicSeries.ID, nil)
		require.NoError(t, err)
		s.deliverAnnounced()

		records := s.newConcertsOf(fan)
		require.Len(t, records, 1)
		assert.Equal(t, []string{publicEvents[0].ID}, records[0].Subject.EventIDs)
		assert.Len(t, s.d.push.to(fan), 1)
	})

	// @spec stories/get-notified-of-new-concerts "Tour found by discovery"
	t.Run("Tour found by discovery", func(t *testing.T) {
		cleanTables(t, db)
		artistID := seedArtist(t, db, "Vaundy", uuid.NewV4().String())
		fan := storyFan(t, db, "tour", "ja", "Asia/Tokyo")
		s := newNewConcertsStory(t, db)
		s.follow(fan, artistID, entity.HypeAway)

		ids := s.discover(artistID, "Vaundy", "ROADSIDE", 3)
		s.deliverAnnounced()

		pushes := s.d.push.to(fan)
		require.Len(t, pushes, 1, "one message for the concerts added together")
		assert.Equal(t, "[Vaundy] 新着ライブ発表!!", pushes[0].Title)
		assert.Equal(t, "新しいライブが3件見つかりました\nROADSIDE", pushes[0].Body)
		assert.Equal(t, "/concerts/"+ids[0], pushes[0].Data[entity.NotificationDataKeyURL], "it links to the earliest concert")
		records := s.newConcertsOf(fan)
		require.Len(t, records, 1)
		assert.ElementsMatch(t, ids, records[0].Subject.EventIDs)
	})
}
