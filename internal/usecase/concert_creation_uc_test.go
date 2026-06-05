package usecase_test

import (
	"context"
	"encoding/json"
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

func (r *fakeVenueRepo) Create(_ context.Context, v *entity.Venue) error {
	r.venues[v.Name] = v
	r.created = append(r.created, v)
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

func (r *fakeSeriesRepo) Get(_ context.Context, _ string) (*entity.Series, error) {
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

func (r *fakeConcertRepo) ListByFollower(_ context.Context, _ string) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) ListByArtists(_ context.Context, _ []string) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) ListByIDs(_ context.Context, _ []string) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) Create(_ context.Context, concerts ...*entity.Concert) ([]string, error) {
	r.created = append(r.created, concerts...)
	// Fake returns all input IDs as "inserted" — tests that need to exercise
	// the dedupe path should override this behaviour.
	ids := make([]string, 0, len(concerts))
	for _, c := range concerts {
		if c != nil {
			ids = append(ids, c.ID)
		}
	}
	return ids, nil
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

func (noopMetrics) RecordConcertSearch(_ context.Context, _ string) {}
func (noopMetrics) RecordFollow(_ context.Context, _ string)        {}
func (noopMetrics) RecordPushSend(_ context.Context, _ string)      {}

// --- tests ---

func TestConcertCreationUseCase_CreateFromDiscovered(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2026, 3, 15, 19, 0, 0, 0, time.UTC)

	t.Run("creates venues and concerts from Places API results", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher()
		ps.places["Venue X"] = &entity.VenuePlace{ExternalID: "place-x", Name: "Venue X Canonical"}
		ps.places["Venue Y"] = &entity.VenuePlace{ExternalID: "place-y", Name: "Venue Y Canonical"}
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-1",
			ArtistName: "Test Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Concert A",
					ListedVenueName: "Venue X",
					LocalDate:       localDate,
					StartTime:       startTime,
					SourceURL:       "https://example.com/a",
				},
				{
					Title:           "Concert B",
					ListedVenueName: "Venue Y",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/b",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		assert.Len(t, venueRepo.created, 2)
		assert.Len(t, concertRepo.created, 2)
		require.NotEmpty(t, concertRepo.created[0].PerformerIDs())
		assert.Equal(t, "artist-1", concertRepo.created[0].PerformerIDs()[0])
		require.NotNil(t, concertRepo.created[0].Series)
		assert.Equal(t, "Concert A", concertRepo.created[0].Series.Title)
	})

	t.Run("reuses existing venue by place_id", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		placeID := "place-existing"
		venueRepo.venues["Existing Venue"] = &entity.Venue{
			ID:            "existing-venue-id",
			Name:          "Existing Venue",
			GooglePlaceID: &placeID,
		}
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher()
		ps.places["Existing Venue"] = &entity.VenuePlace{ExternalID: placeID, Name: "Existing Venue"}
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-2",
			ArtistName: "Another Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Concert C",
					ListedVenueName: "Existing Venue",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/c",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		assert.Empty(t, venueRepo.created)
		require.Len(t, concertRepo.created, 1)
		assert.Equal(t, "existing-venue-id", concertRepo.created[0].VenueID)
	})

	t.Run("deduplicates venues within batch by place_id", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher()
		ps.places["Same Venue"] = &entity.VenuePlace{ExternalID: "place-same", Name: "Same Venue Canonical"}
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-3",
			ArtistName: "Third Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Show 1",
					ListedVenueName: "Same Venue",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/1",
				},
				{
					Title:           "Show 2",
					ListedVenueName: "Same Venue",
					LocalDate:       localDate.AddDate(0, 0, 1),
					SourceURL:       "https://example.com/2",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		assert.Len(t, venueRepo.created, 1)
		require.Len(t, concertRepo.created, 2)
		assert.Equal(t, concertRepo.created[0].VenueID, concertRepo.created[1].VenueID)
	})

	t.Run("skips concerts when Places API returns NotFound", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher()
		ps.places["Known Venue"] = &entity.VenuePlace{ExternalID: "place-known", Name: "Known Venue"}
		// "Unknown Venue" is NOT in ps.places → SearchPlace returns NotFound
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-4",
			ArtistName: "Fourth Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Concert at Known",
					ListedVenueName: "Known Venue",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/known",
				},
				{
					Title:           "Concert at Unknown",
					ListedVenueName: "Unknown Venue",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/unknown",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		// Only the known venue concert should be created.
		assert.Len(t, venueRepo.created, 1)
		require.Len(t, concertRepo.created, 1)
		require.NotNil(t, concertRepo.created[0].Series)
		assert.Equal(t, "Concert at Known", concertRepo.created[0].Series.Title)
	})

	t.Run("skips concert with empty venue name without poisoning the batch", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher()
		ps.places["Known Venue"] = &entity.VenuePlace{ExternalID: "place-known", Name: "Known Venue"}
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-empty-venue",
			ArtistName: "Edge Case Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Valid Show",
					ListedVenueName: "Known Venue",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/valid",
				},
				{
					Title:           "TBA Show",
					ListedVenueName: "",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/tba",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		// Only the valid-venue concert persists; the empty-name entry is skipped.
		assert.Len(t, venueRepo.created, 1)
		require.Len(t, concertRepo.created, 1)
		require.NotNil(t, concertRepo.created[0].Series)
		assert.Equal(t, "Valid Show", concertRepo.created[0].Series.Title)
	})

	t.Run("skips all concerts when all venues are not found", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher() // empty — all venues return NotFound
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-5",
			ArtistName: "Fifth Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Show A",
					ListedVenueName: "Nowhere",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/nowhere",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		assert.Empty(t, venueRepo.created)
		assert.Empty(t, concertRepo.created)
	})
}

func TestConcertCreationUseCase_ResolveVenue_DBFirstLookup(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	t.Run("DB hit by listed name: Places API not called", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		listedName := "武道館"
		placeID := "place-budokan"
		// Pre-seed a venue with listed_venue_name set.
		venueRepo.venues["Nippon Budokan"] = &entity.Venue{
			ID:              "budokan-id",
			Name:            "Nippon Budokan",
			GooglePlaceID:   &placeID,
			ListedVenueName: &listedName,
		}
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		// placeSearcher has no entry for 武道館; if called it returns NotFound → concert would be skipped.
		ps := newStubPlaceSearcher()
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-db-hit",
			ArtistName: "DB Hit Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Budokan Show",
					ListedVenueName: "武道館",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/budokan",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		// Concert created using the DB-found venue, no new venue created.
		assert.Empty(t, venueRepo.created)
		require.Len(t, concertRepo.created, 1)
		assert.Equal(t, "budokan-id", concertRepo.created[0].VenueID)
	})

	t.Run("DB miss by listed name: Places API called, new venue created", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher()
		ps.places["Zepp Tokyo"] = &entity.VenuePlace{ExternalID: "place-zepp-tokyo", Name: "Zepp DiverCity Tokyo"}
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-api-hit",
			ArtistName: "API Hit Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Zepp Show",
					ListedVenueName: "Zepp Tokyo",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/zepp",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		require.Len(t, venueRepo.created, 1)
		assert.Equal(t, "Zepp DiverCity Tokyo", venueRepo.created[0].Name)
		require.Len(t, concertRepo.created, 1)
	})

	t.Run("batch-local cache hit: same listed name deduped within batch without DB or API", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher()
		ps.places["Zepp Osaka"] = &entity.VenuePlace{ExternalID: "place-zepp-osaka", Name: "Zepp Namba Osaka"}
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-batch",
			ArtistName: "Batch Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Night 1",
					ListedVenueName: "Zepp Osaka",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/n1",
				},
				{
					Title:           "Night 2",
					ListedVenueName: "Zepp Osaka",
					LocalDate:       localDate.AddDate(0, 0, 1),
					SourceURL:       "https://example.com/n2",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		// Only one venue created for two concerts with the same listed name.
		assert.Len(t, venueRepo.created, 1)
		require.Len(t, concertRepo.created, 2)
		assert.Equal(t, concertRepo.created[0].VenueID, concertRepo.created[1].VenueID)
	})
}

func TestNewConcertCreationUseCase_PanicsOnNilPlaceSearcher(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		usecase.NewConcertCreationUseCase(newFakeVenueRepo(), &fakeSeriesRepo{}, &fakeConcertRepo{}, nil, messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))
	})
}

func TestConcertCreationUseCase_EventPublishing(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	t.Run("publishes concert.created event with exact concert IDs when concerts are created", func(t *testing.T) {
		t.Parallel()

		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher()
		ps.places["Venue A"] = &entity.VenuePlace{ExternalID: "place-a", Name: "Venue A Canonical"}
		ps.places["Venue B"] = &entity.VenuePlace{ExternalID: "place-b", Name: "Venue B Canonical"}

		// Subscribe before triggering the publish so no messages are lost.
		ctx := context.Background()
		msgCh, err := pub.Subscribe(ctx, entity.SubjectConcertCreated)
		require.NoError(t, err)

		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-pub",
			ArtistName: "Publish Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Show A",
					ListedVenueName: "Venue A",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/a",
				},
				{
					Title:           "Show B",
					ListedVenueName: "Venue B",
					LocalDate:       localDate.AddDate(0, 0, 1),
					SourceURL:       "https://example.com/b",
				},
			},
		}

		err = uc.CreateFromDiscovered(ctx, data)
		require.NoError(t, err)
		require.Len(t, concertRepo.created, 2)

		// Receive the published event.
		select {
		case msg := <-msgCh:
			msg.Ack()
			var published usecase.ConcertCreatedData
			require.NoError(t, json.Unmarshal(msg.Payload, &published))
			assert.Equal(t, "artist-pub", published.ArtistID)
			assert.ElementsMatch(t, []string{concertRepo.created[0].ID, concertRepo.created[1].ID}, published.ConcertIDs)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concert.created event")
		}
	})

	t.Run("does not publish event when zero concerts are created", func(t *testing.T) {
		t.Parallel()

		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		// placeSearcher has no entries — all venue lookups return NotFound → all concerts skipped.
		ps := newStubPlaceSearcher()

		ctx := context.Background()
		msgCh, err := pub.Subscribe(ctx, entity.SubjectConcertCreated)
		require.NoError(t, err)

		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID:   "artist-nopub",
			ArtistName: "No Publish Artist",
			Concerts: entity.ScrapedConcerts{
				{
					Title:           "Unresolvable Show",
					ListedVenueName: "Unknown Venue",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/nopub",
				},
			},
		}

		err = uc.CreateFromDiscovered(ctx, data)
		require.NoError(t, err)
		assert.Empty(t, concertRepo.created)

		// No event should arrive within a short window.
		select {
		case msg := <-msgCh:
			t.Fatalf("unexpected concert.created event received: %s", msg.Payload)
		case <-time.After(100 * time.Millisecond):
			// Correct — no event published.
		}
	})
}

// TestConcertCreationUseCase_SeriesGrouping covers the auto-discovery-series-grouping
// behaviour: tour stops fold into one TOUR series, series identity is adopted
// from already-persisted events, a later-announced start_at fills the existing
// row instead of duplicating, and matinee/evening shows stay distinct.
func TestConcertCreationUseCase_SeriesGrouping(t *testing.T) {
	t.Parallel()

	day1 := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	day3 := time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC)
	matinee := time.Date(2026, 3, 15, 13, 0, 0, 0, time.UTC)
	evening := time.Date(2026, 3, 15, 18, 0, 0, 0, time.UTC)

	// preseedVenue returns a venueRepo whose GetByListedName resolves the given
	// listed names to deterministic IDs (so existing events can be keyed on them).
	preseedVenue := func(byName map[string]string) *fakeVenueRepo {
		r := newFakeVenueRepo()
		for name, id := range byName {
			ln := name
			r.venues[name] = &entity.Venue{ID: id, Name: name, ListedVenueName: &ln}
		}
		return r
	}

	t.Run("multi-stop tour folds into one TOUR series", func(t *testing.T) {
		t.Parallel()
		venueRepo := preseedVenue(map[string]string{"Hall 1": "v1", "Hall 2": "v2", "Hall 3": "v3"})
		concertRepo := &fakeConcertRepo{}
		seriesRepo := &fakeSeriesRepo{}
		uc := usecase.NewConcertCreationUseCase(venueRepo, seriesRepo, concertRepo, newStubPlaceSearcher(), messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "TOUR 2026", ListedVenueName: "Hall 1", LocalDate: day1, IsTour: true, TourGroup: 1, SourceURL: "https://x/tour"},
				{Title: "TOUR 2026", ListedVenueName: "Hall 2", LocalDate: day2, IsTour: true, TourGroup: 1, SourceURL: "https://x/tour"},
				{Title: "TOUR 2026", ListedVenueName: "Hall 3", LocalDate: day3, IsTour: true, TourGroup: 1, SourceURL: "https://x/tour"},
			},
		}
		require.NoError(t, uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, seriesRepo.created, 1, "one TOUR series for the whole tour")
		assert.Equal(t, entity.SeriesTypeTour, seriesRepo.created[0].Type)
		require.Len(t, concertRepo.created, 3)
		for _, c := range concertRepo.created {
			assert.Equal(t, seriesRepo.created[0].ID, c.SeriesID, "all stops share the tour series_id")
		}
	})

	t.Run("re-discovery adopts the existing series", func(t *testing.T) {
		t.Parallel()
		venueRepo := preseedVenue(map[string]string{"Hall 1": "v1", "Hall 2": "v2"})
		start := matinee
		concertRepo := &fakeConcertRepo{
			existing: map[string][]*entity.Event{
				"v1|2026-03-15": {{ID: "ev-old", SeriesID: "series-old", VenueID: "v1", LocalDate: day1, StartTime: &start}},
			},
		}
		seriesRepo := &fakeSeriesRepo{}
		uc := usecase.NewConcertCreationUseCase(venueRepo, seriesRepo, concertRepo, newStubPlaceSearcher(), messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "TOUR 2026", ListedVenueName: "Hall 1", LocalDate: day1, StartTime: matinee, IsTour: true, TourGroup: 1},
				{Title: "TOUR 2026", ListedVenueName: "Hall 2", LocalDate: day2, IsTour: true, TourGroup: 1},
			},
		}
		require.NoError(t, uc.CreateFromDiscovered(context.Background(), data))

		assert.Empty(t, seriesRepo.created, "adopted series — no new series row minted")
		require.Len(t, concertRepo.created, 2)
		for _, c := range concertRepo.created {
			assert.Equal(t, "series-old", c.SeriesID, "tour adopts the existing event's series_id")
		}
	})

	t.Run("later-announced start_at fills the existing row", func(t *testing.T) {
		t.Parallel()
		venueRepo := preseedVenue(map[string]string{"Hall 1": "v1"})
		concertRepo := &fakeConcertRepo{
			existing: map[string][]*entity.Event{
				"v1|2026-03-15": {{ID: "ev-old", SeriesID: "series-old", VenueID: "v1", LocalDate: day1, StartTime: nil}},
			},
		}
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, newStubPlaceSearcher(), messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "Solo", ListedVenueName: "Hall 1", LocalDate: day1, StartTime: evening},
			},
		}
		require.NoError(t, uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, concertRepo.filledIDs, 1, "the unknown-start row is filled, not duplicated")
		assert.Equal(t, "ev-old", concertRepo.filledIDs[0])
		require.Len(t, concertRepo.filledStarts, 1)
		require.NotNil(t, concertRepo.filledStarts[0])
		assert.True(t, concertRepo.filledStarts[0].Equal(evening))
	})

	t.Run("matinee and evening stay distinct", func(t *testing.T) {
		t.Parallel()
		venueRepo := preseedVenue(map[string]string{"Hall 1": "v1"})
		concertRepo := &fakeConcertRepo{}
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, newStubPlaceSearcher(), messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "Matinee", ListedVenueName: "Hall 1", LocalDate: day1, StartTime: matinee},
				{Title: "Evening", ListedVenueName: "Hall 1", LocalDate: day1, StartTime: evening},
			},
		}
		require.NoError(t, uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, concertRepo.created, 2, "same venue+date, different start → two events")
		require.NotNil(t, concertRepo.created[0].StartTime)
		require.NotNil(t, concertRepo.created[1].StartTime)
		assert.False(t, concertRepo.created[0].StartTime.Equal(*concertRepo.created[1].StartTime))
	})

	t.Run("SeriesType from block: tour→TOUR, standalone→SINGLE", func(t *testing.T) {
		t.Parallel()
		venueRepo := preseedVenue(map[string]string{"Hall 1": "v1", "Hall 2": "v2"})
		seriesRepo := &fakeSeriesRepo{}
		uc := usecase.NewConcertCreationUseCase(venueRepo, seriesRepo, &fakeConcertRepo{}, newStubPlaceSearcher(), messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "Single-date Tour", ListedVenueName: "Hall 1", LocalDate: day1, StartTime: evening, IsTour: true, TourGroup: 1},
				{Title: "Standalone", ListedVenueName: "Hall 2", LocalDate: day2, StartTime: evening, IsTour: false},
			},
		}
		require.NoError(t, uc.CreateFromDiscovered(context.Background(), data))

		byType := map[entity.SeriesType]int{}
		for _, s := range seriesRepo.created {
			byType[s.Type]++
		}
		assert.Equal(t, 1, byType[entity.SeriesTypeTour], "single-date <tour> still yields TOUR")
		assert.Equal(t, 1, byType[entity.SeriesTypeSingle], "standalone yields SINGLE")
	})

	t.Run("co-headliner with unknown start attaches to the existing unknown-start row", func(t *testing.T) {
		t.Parallel()
		venueRepo := preseedVenue(map[string]string{"Hall 1": "v1"})
		// Artist Y already persisted this 対バン with no published start time.
		concertRepo := &fakeConcertRepo{
			existing: map[string][]*entity.Event{
				"v1|2026-03-15": {{ID: "ev-old", SeriesID: "series-old", VenueID: "v1", LocalDate: day1, StartTime: nil}},
			},
		}
		seriesRepo := &fakeSeriesRepo{}
		uc := usecase.NewConcertCreationUseCase(venueRepo, seriesRepo, concertRepo, newStubPlaceSearcher(), messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))

		// Artist X is discovered for the same bill, also without a start time.
		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-X",
			Concerts: entity.ScrapedConcerts{
				{Title: "2-man", ListedVenueName: "Hall 1", LocalDate: day1},
			},
		}
		require.NoError(t, uc.CreateFromDiscovered(context.Background(), data))

		// X must NOT be dropped: a concert is built so the performer JOIN attaches
		// X to the existing event; it adopts the existing series and mints nothing.
		require.Len(t, concertRepo.created, 1, "unknown-start co-headliner is built, not skipped")
		assert.Equal(t, "series-old", concertRepo.created[0].SeriesID)
		assert.Empty(t, seriesRepo.created, "adopts the existing series, no new series")
		assert.Empty(t, concertRepo.filledIDs, "both unknown — exact NULL match, not a fill")
	})

	t.Run("unrelated show at same venue+date different start is NOT merged", func(t *testing.T) {
		t.Parallel()
		venueRepo := preseedVenue(map[string]string{"Hall 1": "v1"})
		// An evening show under its own series already exists.
		evStart := evening
		concertRepo := &fakeConcertRepo{
			existing: map[string][]*entity.Event{
				"v1|2026-03-15": {{ID: "ev-evening", SeriesID: "series-evening", VenueID: "v1", LocalDate: day1, StartTime: &evStart}},
			},
		}
		seriesRepo := &fakeSeriesRepo{}
		uc := usecase.NewConcertCreationUseCase(venueRepo, seriesRepo, concertRepo, newStubPlaceSearcher(), messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))

		// A genuinely different matinee at the same venue/date.
		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "Matinee", ListedVenueName: "Hall 1", LocalDate: day1, StartTime: matinee},
			},
		}
		require.NoError(t, uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, concertRepo.created, 1)
		assert.NotEqual(t, "series-evening", concertRepo.created[0].SeriesID, "different start → not merged into the evening series")
		require.Len(t, seriesRepo.created, 1, "mints its own series")
		assert.Empty(t, concertRepo.filledIDs, "different known start is not a fill of the evening row")
	})

	t.Run("within-batch unknown-start is skipped when a known-start sibling covers the same venue/date", func(t *testing.T) {
		t.Parallel()
		venueRepo := preseedVenue(map[string]string{"Hall 1": "v1"})
		concertRepo := &fakeConcertRepo{} // no pre-existing DB rows
		uc := usecase.NewConcertCreationUseCase(venueRepo, &fakeSeriesRepo{}, concertRepo, newStubPlaceSearcher(), messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))

		// One batch lists the same show twice at the same venue/date: once with a
		// start time, once without. The unknown-start one must NOT create a
		// phantom NULL-start row beside the known one.
		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "Show (TBA)", ListedVenueName: "Hall 1", LocalDate: day1},
				{Title: "Show", ListedVenueName: "Hall 1", LocalDate: day1, StartTime: evening},
			},
		}
		require.NoError(t, uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, concertRepo.created, 1, "only the known-start show is built; the unknown-start duplicate is skipped")
		require.NotNil(t, concertRepo.created[0].StartTime)
		assert.True(t, concertRepo.created[0].StartTime.Equal(evening))
	})
}
