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

type fakeConcertRepo struct {
	created []*entity.Concert
}

func (r *fakeConcertRepo) ListByArtist(_ context.Context, _ string, _ bool) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) ListByFollower(_ context.Context, _ string) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) ListByArtists(_ context.Context, _ []string) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) Create(_ context.Context, concerts ...*entity.Concert) error {
	r.created = append(r.created, concerts...)
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
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

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
		assert.Equal(t, "artist-1", concertRepo.created[0].ArtistID)
		assert.Equal(t, "Concert A", concertRepo.created[0].Title)
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
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

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
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

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
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

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
		assert.Equal(t, "Concert at Known", concertRepo.created[0].Title)
	})

	t.Run("skips all concerts when all venues are not found", func(t *testing.T) {
		t.Parallel()
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		ps := newStubPlaceSearcher() // empty — all venues return NotFound
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

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
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

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
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

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
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, ps, messaging.NewEventPublisher(pub), newTestLogger(t))

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
		usecase.NewConcertCreationUseCase(newFakeVenueRepo(), &fakeConcertRepo{}, nil, messaging.NewEventPublisher(newGoChannelPub(t)), newTestLogger(t))
	})
}
