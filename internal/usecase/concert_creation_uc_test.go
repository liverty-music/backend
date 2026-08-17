package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test doubles ---

type fakeVenueRepo struct {
	venues  map[string]*entity.Venue
	created []*entity.Venue
}

func newFakeVenueRepo() *fakeVenueRepo {
	return &fakeVenueRepo{venues: make(map[string]*entity.Venue)}
}

func (r *fakeVenueRepo) Create(_ context.Context, v *entity.Venue) (string, error) {
	r.venues[v.Name] = v
	r.created = append(r.created, v)
	return v.ID, nil
}

func (r *fakeVenueRepo) BackfillPlaceID(_ context.Context, venueID, placeID string) error {
	for _, v := range r.venues {
		if v.ID == venueID {
			if v.GooglePlaceID == nil {
				pid := placeID
				v.GooglePlaceID = &pid
			}
			return nil
		}
	}
	return nil
}

func (r *fakeVenueRepo) Get(_ context.Context, id string) (*entity.Venue, error) {
	for _, v := range r.venues {
		if v.ID == id {
			return v, nil
		}
	}
	return nil, apperr.New(codes.NotFound, "venue not found")
}

func (r *fakeVenueRepo) GetByPlaceID(_ context.Context, placeID string) (*entity.Venue, error) {
	for _, v := range r.venues {
		if v.GooglePlaceID != nil && *v.GooglePlaceID == placeID {
			return v, nil
		}
	}
	return nil, apperr.New(codes.NotFound, "venue not found")
}

func (r *fakeVenueRepo) GetByListedName(_ context.Context, listedVenueName string, adminArea *string) (*entity.Venue, error) {
	for _, v := range r.venues {
		if v.ListedVenueName == nil || *v.ListedVenueName != listedVenueName {
			continue
		}
		if adminArea == nil && v.AdminArea == nil {
			return v, nil
		}
		if adminArea != nil && v.AdminArea != nil && *adminArea == *v.AdminArea {
			return v, nil
		}
	}
	return nil, apperr.New(codes.NotFound, "venue not found")
}

type fakeSeriesRepo struct {
	created []*entity.Series
	// byID lets tests seed series so Get resolves a title (e.g. for the
	// duplicate-conflict preview). Nil map → Get returns NotFound.
	byID map[string]*entity.Series
}

func (r *fakeSeriesRepo) Create(_ context.Context, series ...*entity.Series) ([]string, error) {
	r.created = append(r.created, series...)
	ids := make([]string, 0, len(series))
	for _, s := range series {
		if s != nil {
			ids = append(ids, s.ID)
		}
	}
	return ids, nil
}

func (r *fakeSeriesRepo) Get(_ context.Context, id string) (*entity.Series, error) {
	if s, ok := r.byID[id]; ok {
		return s, nil
	}
	return nil, apperr.New(codes.NotFound, "series not found")
}

func (r *fakeSeriesRepo) ListByIDs(_ context.Context, _ []string) ([]*entity.Series, error) {
	return nil, nil
}

func (r *fakeSeriesRepo) ListSeriesInMerchWindow(_ context.Context, _ time.Duration) ([]*entity.MerchCandidate, error) {
	return nil, nil
}

func (r *fakeSeriesRepo) SetMerchURL(_ context.Context, _, _ string) error {
	return nil
}

func (r *fakeSeriesRepo) ClearMerchURL(_ context.Context, _ string) error {
	return nil
}

type fakeConcertRepo struct {
	created []*entity.Concert
	// existing maps "venueID|YYYY-MM-DD" → events FindEventsByVenueAndDate returns,
	// letting tests exercise series adoption and start-time fill.
	existing map[string][]*entity.Event
	// filledIDs / filledStarts capture FillEventStartTimes calls.
	filledIDs    []string
	filledStarts []*time.Time
	// updatedListedNames maps eventID → the listed_venue_name written via
	// UpdateEventListedVenueName; adopt-staged tests assert on this.
	updatedListedNames map[string]string
	// published holds concerts returned by List; admin tests seed this directly.
	published []*entity.Concert
	// deleteAndSuppressCalled records whether DeleteAndSuppress was invoked, and
	// suppressedEventIDs captures the ids passed; admin Delete tests assert on these.
	deleteAndSuppressCalled bool
	suppressedEventIDs      []string
}

func (r *fakeConcertRepo) ListByArtist(_ context.Context, _ string, _ bool) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) FindEventsByVenueAndDate(_ context.Context, venueIDs []string, dates []time.Time) ([]*entity.Event, error) {
	if r.existing == nil {
		return nil, nil
	}
	seen := make(map[string]bool)
	var out []*entity.Event
	for i := range venueIDs {
		k := venueIDs[i] + "|" + dates[i].Format("2006-01-02")
		for _, e := range r.existing[k] {
			if !seen[e.ID] {
				seen[e.ID] = true
				out = append(out, e)
			}
		}
	}
	return out, nil
}

func (r *fakeConcertRepo) FillEventStartTimes(_ context.Context, eventIDs []string, startTimes, _ []*time.Time) error {
	r.filledIDs = append(r.filledIDs, eventIDs...)
	r.filledStarts = append(r.filledStarts, startTimes...)
	return nil
}

func (r *fakeConcertRepo) UpdateEventListedVenueName(_ context.Context, eventID string, listedVenueName string) error {
	if r.updatedListedNames == nil {
		r.updatedListedNames = make(map[string]string)
	}
	r.updatedListedNames[eventID] = listedVenueName
	return nil
}

func (r *fakeConcertRepo) ListByFollower(_ context.Context, _ string, _ *time.Time) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) ListByArtists(_ context.Context, _ []string) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) ListByLocation(_ context.Context, _ *entity.GeoLocation, _, _ time.Time) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) ListByIDs(_ context.Context, _ []string) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) Create(_ context.Context, concerts ...*entity.Concert) ([]string, error) {
	r.created = append(r.created, concerts...)
	ids := make([]string, 0, len(concerts))
	for _, c := range concerts {
		if c != nil {
			ids = append(ids, c.ID)
		}
	}
	return ids, nil
}

// List returns all concerts in the published slice. Implements the admin
// catalog-listing half of entity.ConcertRepository.
func (r *fakeConcertRepo) List(_ context.Context) ([]*entity.Concert, error) {
	return r.published, nil
}

// Delete removes a concert from the published slice by event id. It is
// idempotent: deleting an absent id is a no-op. Records the call so tests can
// assert repo.Delete was (or was not) invoked.
func (r *fakeConcertRepo) Delete(_ context.Context, eventID string) error {
	for i, c := range r.published {
		if c.ID == eventID {
			r.published = append(r.published[:i], r.published[i+1:]...)
			return nil
		}
	}
	return nil
}

// DeleteAndSuppress removes a published concert and records the suppression by
// event id. It mirrors Delete's idempotent removal and records the call so admin
// Delete tests can assert the suppression path was taken.
func (r *fakeConcertRepo) DeleteAndSuppress(_ context.Context, eventID string) error {
	r.deleteAndSuppressCalled = true
	for i, c := range r.published {
		if c.ID == eventID {
			r.suppressedEventIDs = append(r.suppressedEventIDs, eventID)
			r.published = append(r.published[:i], r.published[i+1:]...)
			return nil
		}
	}
	return nil
}

// fakeStagedConcertRepo is an in-memory implementation for unit tests.
type fakeStagedConcertRepo struct {
	upserted []*entity.StagedConcert
}

func (r *fakeStagedConcertRepo) Upsert(_ context.Context, sc *entity.StagedConcert) error {
	r.upserted = append(r.upserted, sc)
	return nil
}

func (r *fakeStagedConcertRepo) ListPending(_ context.Context) ([]*entity.StagedConcert, error) {
	return r.upserted, nil
}

func (r *fakeStagedConcertRepo) GetByID(_ context.Context, id string) (*entity.StagedConcert, error) {
	for _, sc := range r.upserted {
		if sc.ID == id {
			return sc, nil
		}
	}
	return nil, apperr.New(codes.NotFound, "staged concert not found")
}

func (r *fakeStagedConcertRepo) Delete(_ context.Context, id string) error {
	for i, sc := range r.upserted {
		if sc.ID == id {
			r.upserted = append(r.upserted[:i], r.upserted[i+1:]...)
			return nil
		}
	}
	return nil
}

func (r *fakeStagedConcertRepo) ListPendingDedupKeysByArtist(_ context.Context, _ string) ([]entity.StagedConcertDedupKey, error) {
	return nil, nil
}

// fakeSuppressedConcertRepo is an in-memory suppression set for unit tests. Tests
// seed suppressed keys via suppress(); the consumer's Exists gate reads them.
type fakeSuppressedConcertRepo struct {
	keys     map[string]bool
	inserted []*entity.SuppressedConcert
}

func newFakeSuppressedConcertRepo() *fakeSuppressedConcertRepo {
	return &fakeSuppressedConcertRepo{keys: make(map[string]bool)}
}

// suppressedKey mirrors the (venue_id, local_date, start_at) natural key with a
// NULL-safe start component so a nil start collapses onto the same slot.
func suppressedKey(venueID string, localDate time.Time, startTime *time.Time) string {
	start := ""
	if startTime != nil && !startTime.IsZero() {
		start = startTime.UTC().Format(time.RFC3339)
	}
	return venueID + "|" + localDate.Format("2006-01-02") + "|" + start
}

// suppress seeds a suppressed natural key for the test.
func (r *fakeSuppressedConcertRepo) suppress(venueID string, localDate time.Time, startTime *time.Time) {
	r.keys[suppressedKey(venueID, localDate, startTime)] = true
}

func (r *fakeSuppressedConcertRepo) Insert(_ context.Context, sc *entity.SuppressedConcert) error {
	r.inserted = append(r.inserted, sc)
	r.keys[suppressedKey(sc.VenueID, sc.LocalEventDate, sc.StartTime)] = true
	return nil
}

func (r *fakeSuppressedConcertRepo) Exists(_ context.Context, venueID string, localDate time.Time, startTime *time.Time) (bool, error) {
	return r.keys[suppressedKey(venueID, localDate, startTime)], nil
}

func (r *fakeSuppressedConcertRepo) Delete(_ context.Context, venueID string, localDate time.Time, startTime *time.Time) error {
	delete(r.keys, suppressedKey(venueID, localDate, startTime))
	return nil
}

// stubPlaceSearcher returns pre-configured results keyed by venue name.
type stubPlaceSearcher struct {
	places map[string]*entity.VenuePlace
}

func newStubPlaceSearcher() *stubPlaceSearcher {
	return &stubPlaceSearcher{places: make(map[string]*entity.VenuePlace)}
}

func (s *stubPlaceSearcher) SearchPlace(_ context.Context, name, _ string) (*entity.VenuePlace, error) {
	if p, ok := s.places[name]; ok {
		return p, nil
	}
	return nil, apperr.New(codes.NotFound, "place not found")
}

// --- helpers ---

func newGoChannelPub(t *testing.T) *gochannel.GoChannel {
	t.Helper()
	return gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 256}, watermill.NopLogger{})
}

// --- helpers ---

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return logger
}

// noopMetrics is a no-op implementation of ConcertMetrics, FollowMetrics, and PushMetrics
// for use in unit tests that do not assert on metric recording.
type noopMetrics struct{}

func (noopMetrics) RecordConcertSearch(_ context.Context, _ string)      {}
func (noopMetrics) RecordFollow(_ context.Context, _ string)             {}
func (noopMetrics) RecordPushSend(_ context.Context, _ string)           {}
func (noopMetrics) RecordDeliveryOutcome(_ context.Context, _, _ string) {}

// --- tests ---

func TestConcertCreationUseCase_CreateFromDiscovered(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2026, 3, 15, 19, 0, 0, 0, time.UTC)

	// creationHarness bundles the usecase with its fakes and the publisher so a
	// test can assert on auto-publish, staging, suppression, and CONCERT.created.
	type creationHarness struct {
		uc         usecase.ConcertCreationUseCase
		staged     *fakeStagedConcertRepo
		venue      *fakeVenueRepo
		concert    *fakeConcertRepo
		series     *fakeSeriesRepo
		suppressed *fakeSuppressedConcertRepo
		place      *stubPlaceSearcher
		pub        *gochannel.GoChannel
	}
	build := func(t *testing.T) *creationHarness {
		t.Helper()
		h := &creationHarness{
			staged:     &fakeStagedConcertRepo{},
			venue:      newFakeVenueRepo(),
			concert:    &fakeConcertRepo{},
			series:     &fakeSeriesRepo{},
			suppressed: newFakeSuppressedConcertRepo(),
			place:      newStubPlaceSearcher(),
			pub:        newGoChannelPub(t),
		}
		h.uc = usecase.NewConcertCreationUseCase(
			h.staged, h.venue, h.concert, h.series, h.suppressed,
			h.place, messaging.NewEventPublisher(h.pub), newTestLogger(t),
		)
		return h
	}

	// seedVenue registers an existing venues row keyed by place id so
	// resolveOrCreateVenue returns a known id (letting tests seed existing events
	// at that venue without minting a fresh uuid they cannot predict).
	seedVenue := func(h *creationHarness, venueID, listedName, placeID string) {
		pid := placeID
		ln := listedName
		h.venue.venues[listedName] = &entity.Venue{
			ID:              venueID,
			Name:            listedName,
			GooglePlaceID:   &pid,
			ListedVenueName: &ln,
		}
	}

	expectNoPublish := func(t *testing.T, h *creationHarness) {
		t.Helper()
		ch, err := h.pub.Subscribe(context.Background(), "CONCERT.created")
		require.NoError(t, err)
		select {
		case msg := <-ch:
			t.Fatalf("unexpected CONCERT.created published: %s", msg.Payload)
		case <-time.After(50 * time.Millisecond):
		}
	}

	t.Run("auto-publishes a genuinely new concert at a resolved venue and emits CONCERT.created", func(t *testing.T) {
		t.Parallel()
		h := build(t)
		h.place.places["Venue X"] = &entity.VenuePlace{ExternalID: "place-x", Name: "Venue X Canonical"}
		ch, err := h.pub.Subscribe(context.Background(), "CONCERT.created")
		require.NoError(t, err)

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "Concert A", ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime, SourceURL: "https://example.com/a"},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		// Published directly: an event was inserted, a venue was created, and the
		// concert was NOT staged.
		require.Len(t, h.concert.created, 1)
		assert.Empty(t, h.staged.upserted, "a genuinely new concert must not be staged")
		assert.Len(t, h.venue.created, 1, "venue is created only on the auto-publish path")

		select {
		case msg := <-ch:
			assert.Contains(t, string(msg.Payload), "artist-1")
		case <-time.After(time.Second):
			t.Fatal("expected CONCERT.created to be published")
		}
	})

	t.Run("stages a same-slot conflict without publishing or inserting", func(t *testing.T) {
		t.Parallel()
		h := build(t)
		h.place.places["Venue X"] = &entity.VenuePlace{ExternalID: "place-x", Name: "Venue X"}
		seedVenue(h, "venue-1", "Venue X", "place-x")
		// An existing published event at the resolved (venue, date, start) → conflict.
		h.concert.existing = map[string][]*entity.Event{
			"venue-1|2026-03-15": {{ID: "event-1", SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate, StartTime: &startTime}},
		}

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "Concert A", ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime, SourceURL: "https://example.com/a"},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, h.staged.upserted, 1, "a conflict must be staged for reconciliation")
		assert.Empty(t, h.concert.created, "a conflict must not be auto-published")
		assert.Empty(t, h.venue.created, "a conflict resolves to the existing venue; no new venue")
		expectNoPublish(t, h)
	})

	t.Run("stages an unresolved venue without creating a venue or publishing", func(t *testing.T) {
		t.Parallel()
		h := build(t)
		// "Nowhere" is not in the place stub → SearchPlace returns NotFound.

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-2",
			Concerts: entity.ScrapedConcerts{
				{Title: "Show A", ListedVenueName: "Nowhere", LocalDate: localDate, SourceURL: "https://example.com/nowhere"},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, h.staged.upserted, 1, "an unresolved venue is staged for review")
		assert.Nil(t, h.staged.upserted[0].ResolvedPlaceID)
		assert.Empty(t, h.venue.created, "no venues row for an unresolved venue")
		assert.Empty(t, h.concert.created)
		expectNoPublish(t, h)
	})

	t.Run("skips a suppressed concert entirely (no publish, no stage)", func(t *testing.T) {
		t.Parallel()
		h := build(t)
		h.place.places["Venue X"] = &entity.VenuePlace{ExternalID: "place-x", Name: "Venue X"}
		seedVenue(h, "venue-1", "Venue X", "place-x")
		h.suppressed.suppress("venue-1", localDate, &startTime)

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-3",
			Concerts: entity.ScrapedConcerts{
				{Title: "Concert A", ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime, SourceURL: "https://example.com/a"},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		assert.Empty(t, h.staged.upserted, "a suppressed concert must not be staged")
		assert.Empty(t, h.concert.created, "a suppressed concert must not be auto-published")
		expectNoPublish(t, h)
	})

	t.Run("known-start fill is routed to the publish path, not staged", func(t *testing.T) {
		t.Parallel()
		h := build(t)
		h.place.places["Venue X"] = &entity.VenuePlace{ExternalID: "place-x", Name: "Venue X"}
		seedVenue(h, "venue-1", "Venue X", "place-x")
		// An existing unknown-start row that the known-start discovery fills (not a conflict).
		h.concert.existing = map[string][]*entity.Event{
			"venue-1|2026-03-15": {{ID: "event-1", SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate, StartTime: nil}},
		}

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-4",
			Concerts: entity.ScrapedConcerts{
				{Title: "Concert A", ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime, SourceURL: "https://example.com/a"},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		// The fill path completes the existing row (via FillEventStartTimes) and does
		// not stage the concert as a conflict.
		assert.Contains(t, h.concert.filledIDs, "event-1", "known-start fill must fill the existing row")
		assert.Empty(t, h.staged.upserted, "a fill is the publish path, not a conflict to stage")
	})

	t.Run("un-suppressing a key re-enables auto-publish on re-discovery", func(t *testing.T) {
		t.Parallel()
		h := build(t)
		h.place.places["Venue X"] = &entity.VenuePlace{ExternalID: "place-x", Name: "Venue X"}
		seedVenue(h, "venue-1", "Venue X", "place-x")
		h.suppressed.suppress("venue-1", localDate, &startTime)
		// Operator removes the suppression entry (the deliberate un-suppress path).
		require.NoError(t, h.suppressed.Delete(context.Background(), "venue-1", localDate, &startTime))

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-6",
			Concerts: entity.ScrapedConcerts{
				{Title: "Concert A", ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime, SourceURL: "https://example.com/a"},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		// No longer suppressed → auto-published again.
		require.Len(t, h.concert.created, 1)
		assert.Empty(t, h.staged.upserted)
	})

	t.Run("skips a concert with empty venue name without poisoning the batch", func(t *testing.T) {
		t.Parallel()
		h := build(t)
		h.place.places["Venue X"] = &entity.VenuePlace{ExternalID: "place-x", Name: "Venue X"}

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-5",
			Concerts: entity.ScrapedConcerts{
				{Title: "Valid Show", ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime, SourceURL: "https://example.com/valid"},
				{Title: "TBA Show", ListedVenueName: "", LocalDate: localDate, SourceURL: "https://example.com/tba"},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		// The empty-venue row is skipped; the valid one is auto-published.
		assert.Len(t, h.concert.created, 1)
		assert.Empty(t, h.staged.upserted)
	})
}

func TestNewConcertCreationUseCase_PanicsOnNilPlaceSearcher(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		usecase.NewConcertCreationUseCase(
			&fakeStagedConcertRepo{}, newFakeVenueRepo(), &fakeConcertRepo{}, &fakeSeriesRepo{},
			newFakeSuppressedConcertRepo(), nil, nil, newTestLogger(t),
		)
	})
}
