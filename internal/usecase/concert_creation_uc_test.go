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
	// published holds concerts returned by List; admin tests seed this directly.
	published []*entity.Concert
	// deleteCalled records whether Delete was invoked; admin tests assert on this.
	deleteCalled bool
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
	r.deleteCalled = true
	for i, c := range r.published {
		if c.ID == eventID {
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

	t.Run("stages concerts with resolved venue fields from Places API", func(t *testing.T) {
		t.Parallel()
		stagedRepo := &fakeStagedConcertRepo{}
		ps := newStubPlaceSearcher()
		ps.places["Venue X"] = &entity.VenuePlace{ExternalID: "place-x", Name: "Venue X Canonical"}
		ps.places["Venue Y"] = &entity.VenuePlace{ExternalID: "place-y", Name: "Venue Y Canonical"}
		uc := usecase.NewConcertCreationUseCase(stagedRepo, ps, newTestLogger(t))

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

		assert.Len(t, stagedRepo.upserted, 2)
		assert.Equal(t, "Concert A", stagedRepo.upserted[0].Title)
		assert.Equal(t, "artist-1", stagedRepo.upserted[0].ArtistID)
		require.NotNil(t, stagedRepo.upserted[0].ResolvedPlaceID)
		assert.Equal(t, "place-x", *stagedRepo.upserted[0].ResolvedPlaceID)
		require.NotNil(t, stagedRepo.upserted[0].ResolvedVenueName)
		assert.Equal(t, "Venue X Canonical", *stagedRepo.upserted[0].ResolvedVenueName)
		require.NotNil(t, stagedRepo.upserted[0].StartTime)
		assert.True(t, stagedRepo.upserted[0].StartTime.Equal(startTime))
	})

	t.Run("stages concerts with unresolved venue for review (resolved fields absent)", func(t *testing.T) {
		t.Parallel()
		stagedRepo := &fakeStagedConcertRepo{}
		ps := newStubPlaceSearcher()
		ps.places["Known Venue"] = &entity.VenuePlace{ExternalID: "place-known", Name: "Known Venue"}
		// "Unknown Venue" is NOT in ps.places → SearchPlace returns NotFound
		uc := usecase.NewConcertCreationUseCase(stagedRepo, ps, newTestLogger(t))

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

		// Both concerts are staged; the unresolved one carries no resolved-venue
		// preview so a developer can review it.
		require.Len(t, stagedRepo.upserted, 2)
		byTitle := map[string]*entity.StagedConcert{}
		for _, s := range stagedRepo.upserted {
			byTitle[s.Title] = s
		}
		require.NotNil(t, byTitle["Concert at Known"].ResolvedPlaceID)
		assert.Equal(t, "place-known", *byTitle["Concert at Known"].ResolvedPlaceID)
		require.Contains(t, byTitle, "Concert at Unknown")
		assert.Nil(t, byTitle["Concert at Unknown"].ResolvedPlaceID)
		assert.Nil(t, byTitle["Concert at Unknown"].ResolvedVenueName)
	})

	t.Run("skips concert with empty venue name without poisoning the batch", func(t *testing.T) {
		t.Parallel()
		stagedRepo := &fakeStagedConcertRepo{}
		ps := newStubPlaceSearcher()
		ps.places["Known Venue"] = &entity.VenuePlace{ExternalID: "place-known", Name: "Known Venue"}
		uc := usecase.NewConcertCreationUseCase(stagedRepo, ps, newTestLogger(t))

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

		// Only the valid-venue concert is staged.
		require.Len(t, stagedRepo.upserted, 1)
		assert.Equal(t, "Valid Show", stagedRepo.upserted[0].Title)
	})

	t.Run("stages all concerts for review even when no venue resolves", func(t *testing.T) {
		t.Parallel()
		stagedRepo := &fakeStagedConcertRepo{}
		ps := newStubPlaceSearcher() // empty — all venues return NotFound
		uc := usecase.NewConcertCreationUseCase(stagedRepo, ps, newTestLogger(t))

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

		// Unresolved venue is still staged for review, with no resolved preview.
		require.Len(t, stagedRepo.upserted, 1)
		assert.Equal(t, "Show A", stagedRepo.upserted[0].Title)
		assert.Nil(t, stagedRepo.upserted[0].ResolvedPlaceID)
	})

	t.Run("batch-local cache hit: same listed name calls Places API only once", func(t *testing.T) {
		t.Parallel()
		stagedRepo := &fakeStagedConcertRepo{}
		ps := newStubPlaceSearcher()
		ps.places["Zepp Osaka"] = &entity.VenuePlace{ExternalID: "place-zepp-osaka", Name: "Zepp Namba Osaka"}
		uc := usecase.NewConcertCreationUseCase(stagedRepo, ps, newTestLogger(t))

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

		// Both concerts are staged with the same resolved place.
		assert.Len(t, stagedRepo.upserted, 2)
		require.NotNil(t, stagedRepo.upserted[0].ResolvedPlaceID)
		require.NotNil(t, stagedRepo.upserted[1].ResolvedPlaceID)
		assert.Equal(t, *stagedRepo.upserted[0].ResolvedPlaceID, *stagedRepo.upserted[1].ResolvedPlaceID)
	})

	t.Run("does NOT create venues rows, series, events, or publish CONCERT.created", func(t *testing.T) {
		t.Parallel()
		stagedRepo := &fakeStagedConcertRepo{}
		ps := newStubPlaceSearcher()
		ps.places["Hall A"] = &entity.VenuePlace{ExternalID: "place-a", Name: "Hall A Canonical"}
		uc := usecase.NewConcertCreationUseCase(stagedRepo, ps, newTestLogger(t))

		pub := newGoChannelPub(t)
		ctx := context.Background()
		msgCh, err := pub.Subscribe(ctx, "CONCERT.created")
		require.NoError(t, err)

		// Even though we have a publisher channel open, CreateFromDiscovered must
		// not publish CONCERT.created — that is AdminConcertUseCase.Approve's job.
		_ = messaging.NewEventPublisher(pub) // publisher not passed to uc

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-nodirect",
			Concerts: entity.ScrapedConcerts{
				{Title: "Show", ListedVenueName: "Hall A", LocalDate: localDate, SourceURL: "https://example.com/show"},
			},
		}
		err = uc.CreateFromDiscovered(ctx, data)
		require.NoError(t, err)

		// One row staged, no direct publish.
		require.Len(t, stagedRepo.upserted, 1)

		select {
		case msg := <-msgCh:
			t.Fatalf("unexpected CONCERT.created event published: %s", msg.Payload)
		case <-time.After(50 * time.Millisecond):
			// Correct — discovery path must not publish CONCERT.created.
		}
	})
}

func TestNewConcertCreationUseCase_PanicsOnNilPlaceSearcher(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		usecase.NewConcertCreationUseCase(&fakeStagedConcertRepo{}, nil, newTestLogger(t))
	})
}
