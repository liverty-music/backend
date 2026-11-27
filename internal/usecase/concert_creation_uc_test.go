package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return logger
}

// notFound is the NotFound error a repository or the place searcher returns
// when it has no record of what.
func notFound(what string) error {
	return apperr.New(codes.NotFound, what+" not found")
}

// anyArgs returns n mock.Anything matchers, for the variadic arguments of a
// mocked call (the mocks unroll variadic arguments, so a call matches only an
// expectation with as many matchers as it has arguments).
func anyArgs(n int) []any {
	args := make([]any, n)
	for i := range args {
		args[i] = mock.Anything
	}
	return args
}

// ingestSignal is one AnalyticsClient.Capture call of the creation usecase.
type ingestSignal struct {
	event usecase.AnalyticsEvent
	// traceID is the trace of the context the signal was sent under.
	traceID string
}

// creationHarness is a ConcertCreationUseCase over mocks. Its expectation
// helpers record what the usecase passes to the mocks, so a test can assert on
// what was stored; a call no test expects fails the test.
type creationHarness struct {
	uc        usecase.ConcertCreationUseCase
	candidate *entitymocks.MockConcertCandidateRepository
	venue     *entitymocks.MockVenueRepository
	concert   *entitymocks.MockConcertRepository
	series    *entitymocks.MockSeriesRepository
	place     *entitymocks.MockVenuePlaceSearcher

	createdSeries   []*entity.Series
	createdVenues   []*entity.Venue
	createdConcerts []*entity.Concert
	// createCalls holds the concerts of each Concert.Create call.
	createCalls [][]*entity.Concert
	staged      []*entity.ConcertCandidate
	signals     []ingestSignal
	// upcomingTours are the Artist's upcoming discovered tours
	// Series.ListUpcomingToursByArtist returns.
	upcomingTours []*entity.Series
}

func newCreationHarness(t *testing.T) *creationHarness {
	t.Helper()
	h := &creationHarness{
		candidate: entitymocks.NewMockConcertCandidateRepository(t),
		venue:     entitymocks.NewMockVenueRepository(t),
		concert:   entitymocks.NewMockConcertRepository(t),
		series:    entitymocks.NewMockSeriesRepository(t),
		place:     entitymocks.NewMockVenuePlaceSearcher(t),
	}
	analytics := ucmocks.NewMockAnalyticsClient(t)
	analytics.EXPECT().Capture(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, e usecase.AnalyticsEvent) error {
			h.signals = append(h.signals, ingestSignal{event: e, traceID: trace.SpanContextFromContext(ctx).TraceID().String()})
			return nil
		}).Maybe()
	h.series.EXPECT().ListUpcomingToursByArtist(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, string, time.Time) ([]*entity.Series, error) {
			return h.upcomingTours, nil
		}).Maybe()
	h.uc = usecase.NewConcertCreationUseCase(
		h.candidate, h.venue, h.concert, h.series,
		h.place, analytics, time.Now, newTestLogger(t),
	)
	return h
}

// ingestReports returns the properties and trace ids of the
// concert.ingest.completed signals sent.
func (h *creationHarness) ingestReports() ([]usecase.AnalyticsProperties, []string) {
	var props []usecase.AnalyticsProperties
	var traces []string
	for _, s := range h.signals {
		if s.event.Name == usecase.EventConcertIngestCompleted {
			props = append(props, s.event.Properties)
			traces = append(traces, s.traceID)
		}
	}
	return props, traces
}

// findsNoArtistEvents expects each of n series to look up the Artist's stored
// Events on its dates and find none, so its series is minted.
func (h *creationHarness) findsNoArtistEvents(n int) {
	h.concert.EXPECT().FindEventsByArtistAndDate(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Times(n)
}

// createsSeries expects n series to be created and records them.
func (h *creationHarness) createsSeries(n int) {
	h.series.EXPECT().Create(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, series ...*entity.Series) ([]string, error) {
			h.createdSeries = append(h.createdSeries, series...)
			return []string{series[0].ID}, nil
		}).Times(n)
}

// sweepsOrphans expects the end-of-batch sweep of the series the batch minted.
func (h *creationHarness) sweepsOrphans() {
	h.series.EXPECT().DeleteOrphaned(mock.Anything, mock.Anything).Return(0, nil).Once()
}

// resolvesPlace expects Google Places to be asked once for the listed venue
// name and to resolve it to place, which lies in Japan unless it says
// otherwise.
func (h *creationHarness) resolvesPlace(name string, place *entity.VenuePlace) {
	if place.CountryCode == "" {
		place.CountryCode = "JP"
	}
	h.place.EXPECT().SearchPlace(mock.Anything, name, "JP-13").Return([]*entity.VenuePlace{place}, nil).Once()
}

// cannotResolvePlace expects Google Places to be asked once for the listed
// venue name and to find no place.
func (h *creationHarness) cannotResolvePlace(name string) {
	h.place.EXPECT().SearchPlace(mock.Anything, name, "JP-13").Return(nil, nil).Once()
}

// createsVenue expects the venue of placeID, stored under neither that place
// id nor listedName, to be created, and records it.
func (h *creationHarness) createsVenue(placeID, listedName string) {
	h.venue.EXPECT().GetByPlaceID(mock.Anything, placeID).Return(nil, notFound("venue")).Once()
	h.venue.EXPECT().GetByListedName(mock.Anything, listedName, "JP-13").Return(nil, notFound("venue")).Once()
	h.venue.EXPECT().Create(mock.Anything, mock.MatchedBy(func(v *entity.Venue) bool {
		return v.GooglePlaceID != nil && *v.GooglePlaceID == placeID
	})).RunAndReturn(func(_ context.Context, v *entity.Venue) (string, error) {
		h.createdVenues = append(h.createdVenues, v)
		return v.ID, nil
	}).Once()
}

// storedVenue expects the venue lookup by placeID to find the stored venue
// venueID.
func (h *creationHarness) storedVenue(placeID, venueID string) {
	h.venue.EXPECT().GetByPlaceID(mock.Anything, placeID).Return(&entity.Venue{ID: venueID, GooglePlaceID: &placeID}, nil).Once()
}

// slotsFree expects n events to find no stored Event at their venue and date.
func (h *creationHarness) slotsFree(n int) {
	h.concert.EXPECT().FindEventsByVenueAndDate(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Times(n)
}

// slotHolds expects the lookup at venueID on date to find events.
func (h *creationHarness) slotHolds(venueID string, date time.Time, events ...*entity.Event) {
	h.concert.EXPECT().FindEventsByVenueAndDate(mock.Anything, []string{venueID}, []time.Time{date}).Return(foundAs(events), nil).Once()
}

// artistSlotHolds expects the lookup at venueID on date to find events the
// Artist performs at.
func (h *creationHarness) artistSlotHolds(artistID, venueID string, date time.Time, events ...*entity.Event) {
	h.concert.EXPECT().FindEventsByVenueAndDate(mock.Anything, []string{venueID}, []time.Time{date}).Return(foundAs(events, artistID), nil).Once()
}

// stages expects n events to be staged as ConcertCandidates and records them.
func (h *creationHarness) stages(n int) {
	h.candidate.EXPECT().Upsert(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, c *entity.ConcertCandidate) error {
			h.staged = append(h.staged, c)
			return nil
		}).Times(n)
}

// publishes expects one Concert.Create call per series, with the given number
// of concerts each, and records them.
func (h *creationHarness) publishes(perSeries ...int) {
	for _, n := range perSeries {
		h.concert.EXPECT().Create(mock.Anything, anyArgs(n)...).
			RunAndReturn(func(_ context.Context, concerts ...*entity.Concert) ([]string, error) {
				h.createCalls = append(h.createCalls, concerts)
				h.createdConcerts = append(h.createdConcerts, concerts...)
				ids := make([]string, 0, len(concerts))
				for _, c := range concerts {
					ids = append(ids, c.ID)
				}
				return ids, nil
			}).Once()
	}
}

// --- tests ---

func TestConcertCreationUseCase_CreateFromDiscovered(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	startTime := todAt(19, 0)

	// A CONCERT.created announcement is recorded only by Concert.Create, so a
	// test that sets no publishes expectation also proves nothing is announced.

	t.Run("auto-publishes a genuinely new concert at a resolved venue", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Genuinely new"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.resolvesPlace("Venue X", &entity.VenuePlace{ProviderUserID: "place-x", Name: "Venue X Canonical"})
		h.createsVenue("place-x", "Venue X")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Series: []*entity.DiscoveredSeries{
				{
					Title:     "Concert A",
					Type:      entity.SeriesTypeSingle,
					SourceURL: "https://example.com/a",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime},
					},
				},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		// Published directly: an event was inserted and a venue was created; the
		// concert was not staged (no Upsert is expected).
		require.Len(t, h.createdConcerts, 1)
		require.Len(t, h.createdSeries, 1)
		assert.Equal(t, h.createdSeries[0].ID, h.createdConcerts[0].SeriesID, "the Event is published under the series' Series")
		assert.Len(t, h.createdVenues, 1, "venue is created only on the auto-publish path")
	})

	t.Run("mixed batch reports what it did", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Mixed batch"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		for _, v := range []string{"Venue A", "Venue B", "Venue C"} {
			h.resolvesPlace(v, &entity.VenuePlace{ProviderUserID: "place-" + v, Name: v})
			h.createsVenue("place-"+v, v)
		}
		// "Venue Unknown" has no place: staged for review.
		h.cannotResolvePlace("Venue Unknown")
		h.slotsFree(3)
		h.stages(1)
		h.publishes(3)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Series: []*entity.DiscoveredSeries{{
				Title: "Tour", Type: entity.SeriesTypeTour, SourceURL: "https://example.com/tour",
				Events: []*entity.DiscoveredEvent{
					{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue A", LocalDate: localDate, StartTime: startTime},
					{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue B", LocalDate: localDate.AddDate(0, 0, 1), StartTime: startTime},
					{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue C", LocalDate: localDate.AddDate(0, 0, 2), StartTime: startTime},
					{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue Unknown", LocalDate: localDate.AddDate(0, 0, 3)},
				},
			}},
		}
		// The message context carries the trace of the search that found the batch.
		ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(context.Background(), "search")
		defer span.End()

		require.NoError(t, h.uc.CreateFromDiscovered(ctx, data))

		require.Len(t, h.createdConcerts, 3)
		require.Len(t, h.staged, 1)
		props, traces := h.ingestReports()
		assert.Equal(t, []usecase.AnalyticsProperties{{
			"artist_id":       "artist-1",
			"outcome":         "completed",
			"published_count": 3,
			"staged_count":    1,
			"filled_count":    0,
			"conflict_count":  0,
		}}, props)
		assert.Equal(t, []string{span.SpanContext().TraceID().String()}, traces)
	})

	t.Run("batch failing part way reports what it did before failing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Batch fails part way"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(2)
		errCreate := errors.New("insert series failed")
		h.series.EXPECT().Create(mock.Anything, mock.MatchedBy(func(s *entity.Series) bool { return s.Title == "First Tour" })).
			RunAndReturn(func(_ context.Context, series ...*entity.Series) ([]string, error) {
				return []string{series[0].ID}, nil
			}).Once()
		h.series.EXPECT().Create(mock.Anything, mock.MatchedBy(func(s *entity.Series) bool { return s.Title == "Second Tour" })).
			Return(nil, errCreate).Once()
		// Every venue of a series is decided before its Series is chosen, so
		// Venue C is resolved before the second Series fails.
		for _, v := range []string{"Venue A", "Venue B", "Venue C"} {
			h.resolvesPlace(v, &entity.VenuePlace{ProviderUserID: "place-" + v, Name: v})
			h.createsVenue("place-"+v, v)
		}
		h.slotsFree(2)
		h.publishes(2)
		h.sweepsOrphans()
		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Series: []*entity.DiscoveredSeries{
				{
					Title: "First Tour", Type: entity.SeriesTypeTour, SourceURL: "https://example.com/first",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue A", LocalDate: localDate, StartTime: startTime},
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue B", LocalDate: localDate.AddDate(0, 0, 1), StartTime: startTime},
					},
				},
				{
					Title: "Second Tour", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/second",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue C", LocalDate: localDate.AddDate(0, 1, 0), StartTime: startTime},
					},
				},
			},
		}

		err := h.uc.CreateFromDiscovered(context.Background(), data)

		require.ErrorIs(t, err, errCreate)
		require.Len(t, h.createdConcerts, 2)
		props, _ := h.ingestReports()
		assert.Equal(t, []usecase.AnalyticsProperties{{
			"artist_id":       "artist-1",
			"outcome":         "failed",
			"published_count": 2,
			"staged_count":    0,
			"filled_count":    0,
			"conflict_count":  0,
		}}, props)
	})

	t.Run("stages a same-slot conflict without publishing or inserting", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Same slot as an existing event"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.resolvesPlace("Venue X", &entity.VenuePlace{ProviderUserID: "place-x", Name: "Venue X"})
		// The place resolves to the stored venue-1, so no venue is created.
		h.storedVenue("place-x", "venue-1")
		// An existing published event at the resolved (venue, date, start) → conflict.
		h.slotHolds("venue-1", localDate, &entity.Event{ID: "event-1", SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate, StartTime: startTime})
		h.stages(1)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Series: []*entity.DiscoveredSeries{
				{
					Title:     "Concert A",
					Type:      entity.SeriesTypeSingle,
					SourceURL: "https://example.com/a",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime},
					},
				},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		// @spec components/usecase/concert/create-from-discovered "Staged collision with a resolved place"
		require.Len(t, h.staged, 1, "a conflict must be staged for reconciliation")
		staged := h.staged[0]
		assert.Equal(t, "Venue X", staged.ListedVenueName)
		assert.Equal(t, "JP-13", *staged.AdminArea)
		assert.Equal(t, "Asia/Tokyo", *staged.TimeZone)
		require.NotNil(t, staged.ResolvedPlaceID)
		assert.Equal(t, "place-x", *staged.ResolvedPlaceID, "only the place id of the map catalog's answer is kept")
	})

	t.Run("a staged collision keeps only the place id of the map catalog's answer", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/concert-candidate "Resolved venue"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.resolvesPlace("Zepp Haneda(TOKYO)", &entity.VenuePlace{
			ProviderUserID: "P", Name: "Zepp Haneda (TOKYO)",
			Coordinates: &entity.Coordinates{Latitude: 35.55, Longitude: 139.78},
		})
		h.storedVenue("P", "venue-1")
		h.slotHolds("venue-1", localDate, &entity.Event{ID: "event-1", SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate, StartTime: startTime})
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Series: []*entity.DiscoveredSeries{{
				Title: "Concert A", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/a",
				Events: []*entity.DiscoveredEvent{{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Zepp Haneda(TOKYO)", LocalDate: localDate, StartTime: startTime}},
			}},
		}))

		require.Len(t, h.staged, 1)
		require.Len(t, h.createdSeries, 1)
		// The whole candidate is compared, so no field holds the catalog's
		// name "Zepp Haneda (TOKYO)" or its coordinates.
		assert.Equal(t, &entity.ConcertCandidate{
			ID:              h.staged[0].ID,
			ArtistID:        "artist-1",
			SeriesID:        h.createdSeries[0].ID,
			Title:           "Concert A",
			LocalDate:       localDate,
			StartTime:       startTime,
			ListedVenueName: "Zepp Haneda(TOKYO)",
			AdminArea:       new("JP-13"),
			TimeZone:        new("Asia/Tokyo"),
			SourceURL:       new("https://example.com/a"),
			ResolvedPlaceID: new("P"),
		}, h.staged[0])
	})

	t.Run("stages an unknown start next to an Event with a known start", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Unknown start next to a known start"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.resolvesPlace("Venue X", &entity.VenuePlace{ProviderUserID: "place-x", Name: "Venue X"})
		h.storedVenue("place-x", "venue-1")
		h.slotHolds("venue-1", localDate, &entity.Event{ID: "event-1", SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate, StartTime: startTime})
		h.stages(1)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Series: []*entity.DiscoveredSeries{{
				Title: "Concert A", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/a",
				Events: []*entity.DiscoveredEvent{{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue X", LocalDate: localDate}},
			}},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		assert.Len(t, h.staged, 1, "an unknown start next to a known start is staged")
	})

	t.Run("publishes a known start next to another Event with no start", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Known start next to an unknown-start event"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.resolvesPlace("Venue X", &entity.VenuePlace{ProviderUserID: "place-x", Name: "Venue X"})
		h.storedVenue("place-x", "venue-1")
		// Another artist's Event at the Venue and date, with no start time; the
		// discovering Artist has no Event there, so nothing is filled (no
		// FillEventStartTimes is expected).
		h.slotHolds("venue-1", localDate, &entity.Event{ID: "event-1", SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate})
		h.publishes(1)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Series: []*entity.DiscoveredSeries{{
				Title: "Concert A", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/a",
				Events: []*entity.DiscoveredEvent{{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime}},
			}},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, h.createdConcerts, 1, "a new Event with the known start is published")
		require.NotNil(t, h.createdConcerts[0].StartTime)
		assert.Equal(t, startTime, h.createdConcerts[0].StartTime, "the new Event has the discovered start")
	})

	t.Run("stages an unresolved venue without creating a venue or publishing", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/concert-candidate "Unresolved venue"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.cannotResolvePlace("Nowhere")
		h.stages(1)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-2",
			Series: []*entity.DiscoveredSeries{
				{
					Title:     "Show A",
					Type:      entity.SeriesTypeSingle,
					SourceURL: "https://example.com/nowhere",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Nowhere", LocalDate: localDate},
					},
				},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		// No venues row is looked up or created for an unresolved venue.
		require.Len(t, h.staged, 1, "an unresolved venue is staged for review")
		assert.Nil(t, h.staged[0].ResolvedPlaceID)
	})

	t.Run("publishes an event at a slot where a Concert was deleted", func(t *testing.T) {
		t.Parallel()
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.resolvesPlace("Venue X", &entity.VenuePlace{ProviderUserID: "place-x", Name: "Venue X"})
		h.storedVenue("place-x", "venue-1")
		// An admin deleted the Concert that held this slot: no Event is left
		// there, and nothing else records the delete.
		h.slotHolds("venue-1", localDate)
		h.publishes(1)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-3",
			Series: []*entity.DiscoveredSeries{
				{
					Title:     "Concert A",
					Type:      entity.SeriesTypeSingle,
					SourceURL: "https://example.com/a",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime},
					},
				},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		assert.Len(t, h.createdConcerts, 1, "the event is published")
		assert.Empty(t, h.staged, "nothing is staged")
	})

	t.Run("known-start fill is routed to the publish path, not staged", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Start time announced"
		h := newCreationHarness(t)
		// The Artist's existing unknown-start row on the date at the same listed
		// venue: the re-discovery fast path adopts its series, skips the Places
		// API, and fills the row's start (not a conflict).
		listedName := "Venue X"
		h.concert.EXPECT().FindEventsByArtistAndDate(mock.Anything, "artist-4", []time.Time{localDate}).
			Return(foundAs([]*entity.Event{{ID: "event-1", SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate, StartTime: nil, ListedVenueName: &listedName}}, "artist-4"), nil).Once()
		h.artistSlotHolds("artist-4", "venue-1", localDate, &entity.Event{ID: "event-1", SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate, ListedVenueName: &listedName})
		h.concert.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-1"}, []*entity.TimeOfDay{startTime}, []*entity.TimeOfDay{nil}).Return(nil).Once()
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-4",
			Series: []*entity.DiscoveredSeries{
				{
					Title:     "Concert A",
					Type:      entity.SeriesTypeSingle,
					SourceURL: "https://example.com/a",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime},
					},
				},
			},
		}

		// The fill completes the existing row (FillEventStartTimes) and stages
		// nothing (no Upsert is expected).
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))
	})

	t.Run("skips a concert with empty venue name without poisoning the batch", func(t *testing.T) {
		t.Parallel()
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.resolvesPlace("Venue X", &entity.VenuePlace{ProviderUserID: "place-x", Name: "Venue X"})
		h.createsVenue("place-x", "Venue X")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-5",
			Series: []*entity.DiscoveredSeries{
				{
					Title:     "Mixed Show",
					Type:      entity.SeriesTypeSingle,
					SourceURL: "https://example.com/valid",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime},
						// TBA event with empty venue name — must be skipped.
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "", LocalDate: localDate},
					},
				},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		// The empty-venue row is skipped; the valid one is auto-published.
		assert.Len(t, h.createdConcerts, 1)
	})

	t.Run("skips an event whose venue is TBA and stores no Venue named TBA", func(t *testing.T) {
		t.Parallel()
		// Follows from entity/venue "Venue not yet announced is missing" and
		// the skip of a blank name ("Venue to be announced").
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		// Google Places is never asked for "TBA" (no SearchPlace expectation).
		h.resolvesPlace("Venue X", &entity.VenuePlace{ProviderUserID: "place-x", Name: "Venue X"})
		h.createsVenue("place-x", "Venue X")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-6",
			Series: []*entity.DiscoveredSeries{
				{
					Title:     "Tour With A TBA Date",
					Type:      entity.SeriesTypeSingle,
					SourceURL: "https://example.com/tba",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue X", LocalDate: localDate, StartTime: startTime},
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "TBA", LocalDate: localDate.AddDate(0, 0, 7)},
					},
				},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, h.createdConcerts, 1, "only the event at a named venue is stored")
		assert.True(t, localDate.Equal(h.createdConcerts[0].LocalDate))
		for _, v := range h.createdVenues {
			assert.NotEqual(t, "TBA", v.Name, "no Venue named TBA is stored")
		}
	})
}

func TestCreateFromDiscovered_RediscoveryOfTheArtistsOwnEvents(t *testing.T) {
	t.Parallel()

	date1 := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	start13 := todAt(13, 0)
	start18 := todAt(18, 0)
	venueA, venueB, venueZ := "Venue A", "Venue B", "Venue Z"

	tour := func(events ...*entity.DiscoveredEvent) entity.ConcertDiscoveredData {
		return entity.ConcertDiscoveredData{
			ArtistID: "artist-tour",
			Series: []*entity.DiscoveredSeries{{
				Title: "Tour 2026", Type: entity.SeriesTypeTour, SourceURL: "https://example.com/tour",
				Events: events,
			}},
		}
	}

	t.Run("a re-discovered tour joins the series of its matching event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Re-discovered tour joins its series"
		h := newCreationHarness(t)
		h.concert.EXPECT().FindEventsByArtistAndDate(mock.Anything, "artist-tour", mock.Anything).
			Return(foundAs([]*entity.Event{{ID: "event-1", SeriesID: "series-1", VenueID: "venue-a", LocalDate: date1, ListedVenueName: &venueA}}, "artist-tour"), nil).Once()
		// Only the new date is created; no Series is (a Series.Create would fail the mock).
		h.artistSlotHolds("artist-tour", "venue-a", date1, &entity.Event{ID: "event-1", SeriesID: "series-1", VenueID: "venue-a", LocalDate: date1, ListedVenueName: &venueA})
		h.resolvesPlace(venueB, &entity.VenuePlace{ProviderUserID: "place-b", Name: venueB})
		h.createsVenue("place-b", venueB)
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), tour(
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: venueA, LocalDate: date1},
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: venueB, LocalDate: date2},
		)))

		assert.Empty(t, h.createdSeries, "no Series is created")
		require.Len(t, h.createdConcerts, 1)
		assert.Equal(t, "series-1", h.createdConcerts[0].SeriesID, "the new date goes under the matched Event's Series")
	})

	t.Run("a same-day show at another venue does not make the tour join", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Same-day show at another venue does not join"
		h := newCreationHarness(t)
		h.concert.EXPECT().FindEventsByArtistAndDate(mock.Anything, "artist-tour", mock.Anything).
			Return(foundAs([]*entity.Event{{ID: "event-9", SeriesID: "series-9", VenueID: "venue-z", LocalDate: date1, ListedVenueName: &venueZ}}, "artist-tour"), nil).Once()
		h.createsSeries(1)
		h.resolvesPlace(venueA, &entity.VenuePlace{ProviderUserID: "place-a", Name: venueA})
		h.createsVenue("place-a", venueA)
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), tour(
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: venueA, LocalDate: date1},
		)))

		require.Len(t, h.createdSeries, 1, "a new Series is created")
		require.Len(t, h.createdConcerts, 1)
		assert.NotEqual(t, "series-9", h.createdConcerts[0].SeriesID)
		assert.Equal(t, h.createdSeries[0].ID, h.createdConcerts[0].SeriesID)
	})

	t.Run("an exact re-discovery stores nothing and looks up no venue", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Exact re-discovery"
		h := newCreationHarness(t)
		h.concert.EXPECT().FindEventsByArtistAndDate(mock.Anything, "artist-tour", mock.Anything).
			Return(foundAs([]*entity.Event{{ID: "event-1", SeriesID: "series-1", VenueID: "venue-a", LocalDate: date1, StartTime: start18, ListedVenueName: &venueA}}, "artist-tour"), nil).Once()
		h.artistSlotHolds("artist-tour", "venue-a", date1, &entity.Event{ID: "event-1", SeriesID: "series-1", VenueID: "venue-a", LocalDate: date1, StartTime: start18, ListedVenueName: &venueA})
		// No Places, venue, Create or Upsert expectation: any such call fails the mock.
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), tour(
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: venueA, LocalDate: date1, StartTime: start18},
		)))

		assert.Empty(t, h.createdConcerts)
		assert.Empty(t, h.staged)
		assert.Empty(t, h.createdVenues)
	})

	t.Run("two new start times fill the unknown-start event and publish the other", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Two new start times for one unknown-start event"
		h := newCreationHarness(t)
		h.concert.EXPECT().FindEventsByArtistAndDate(mock.Anything, "artist-tour", mock.Anything).
			Return(foundAs([]*entity.Event{{ID: "event-1", SeriesID: "series-1", VenueID: "venue-a", LocalDate: date1, ListedVenueName: &venueA}}, "artist-tour"), nil).Once()
		h.concert.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-1"}, []*entity.TimeOfDay{start13}, []*entity.TimeOfDay{nil}).Return(nil).Once()
		// The venue is the matched Event's: no Places or venue lookup is expected.
		// The first event finds the Event without a start time, the second
		// finds it filled with 13:00.
		h.artistSlotHolds("artist-tour", "venue-a", date1, &entity.Event{ID: "event-1", SeriesID: "series-1", VenueID: "venue-a", LocalDate: date1, ListedVenueName: &venueA})
		h.artistSlotHolds("artist-tour", "venue-a", date1, &entity.Event{ID: "event-1", SeriesID: "series-1", VenueID: "venue-a", LocalDate: date1, StartTime: start13, ListedVenueName: &venueA})
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), tour(
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: venueA, LocalDate: date1, StartTime: start13},
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: venueA, LocalDate: date1, StartTime: start18},
		)))

		require.Len(t, h.createdConcerts, 1, "the 18:00 show is a new event")
		require.NotNil(t, h.createdConcerts[0].StartTime)
		assert.Equal(t, start18, h.createdConcerts[0].StartTime)
		assert.Equal(t, "series-1", h.createdConcerts[0].SeriesID)
	})
}

func TestNewConcertCreationUseCase_PanicsOnNilPlaceSearcher(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		usecase.NewConcertCreationUseCase(
			entitymocks.NewMockConcertCandidateRepository(t), entitymocks.NewMockVenueRepository(t),
			entitymocks.NewMockConcertRepository(t), entitymocks.NewMockSeriesRepository(t),
			nil, nil, time.Now, newTestLogger(t),
		)
	})
}

// TestCreateFromDiscovered_MultiDateTour_PersistsSingleTourSeries is the
// regression guard for the series-fragmentation bug: a multi-date tour discovered
// as one DiscoveredSeries must persist exactly one series row of type TOUR, with
// all member events sharing that single SeriesID, stored in one Concert.Create
// call. The pre-fix code minted one series per event, causing 1-event series
// proliferation in production.
func TestCreateFromDiscovered_MultiDateTour_PersistsSingleTourSeries(t *testing.T) {
	t.Parallel()

	// Three dates, each at a different venue; all resolved via Google Places.
	date1 := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	date3 := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)

	t.Run("three-date tour produces exactly one TOUR series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "New tour"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		// Exactly one series row minted (not one per event): a second
		// Series.Create would fail the mock.
		h.createsSeries(1)
		for _, v := range []string{"A", "B", "C"} {
			h.resolvesPlace("Venue "+v, &entity.VenuePlace{ProviderUserID: "place-" + v, Name: "Venue " + v + " Canonical"})
			h.createsVenue("place-"+v, "Venue "+v)
		}
		h.slotsFree(3)
		h.publishes(3)
		h.sweepsOrphans()

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-tour",
			Series: []*entity.DiscoveredSeries{
				{
					Title:     "Eras Tour 2026",
					Type:      entity.SeriesTypeTour,
					SourceURL: "https://example.com/tour",
					Events: []*entity.DiscoveredEvent{
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue A", LocalDate: date1},
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue B", LocalDate: date2},
						{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue C", LocalDate: date3},
					},
				},
			},
		}
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), data))

		require.Len(t, h.createdSeries, 1, "one DiscoveredSeries must produce exactly one series row")
		assert.Equal(t, entity.SeriesTypeTour, h.createdSeries[0].Type, "series type must be TOUR")
		assert.Equal(t, "Eras Tour 2026", h.createdSeries[0].Title)

		// All three events inserted, each under the single minted series ID;
		// nothing staged (no Upsert is expected).
		require.Len(t, h.createdConcerts, 3, "all three tour dates must be inserted")
		sharedSeriesID := h.createdSeries[0].ID
		for _, c := range h.createdConcerts {
			assert.Equal(t, sharedSeriesID, c.SeriesID,
				"every concert must share the single tour series ID (got %q, want %q)", c.SeriesID, sharedSeriesID)
		}
	})
}

// TestCreateFromDiscovered_ApprovePreservesTourSeriesID verifies that approving a
// candidate that already carries a SeriesID (set at discovery time) inserts the
// event under that existing series and does NOT create a new series row. This guards
// the invariant that the admin approval path never mints a series — only the
// discovery path does.
func TestCreateFromDiscovered_ApprovePreservesTourSeriesID(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-tour", Name: "Tour Artist", MBID: "22222222-2222-2222-2222-222222222222"}
	const tourSeriesID = "series-tour-existing"

	// @spec components/usecase/concert-candidate/approve "Tour stop joins its tour"
	t.Run("approve inserts under the existing tour series ID without minting a new series", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)

		sourceURL := "https://example.com/tour"
		sc := &entity.ConcertCandidate{
			ID:              "candidate-tour-event",
			ArtistID:        artist.ID,
			SeriesID:        tourSeriesID,
			Title:           "Eras Tour 2026",
			LocalDate:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			ListedVenueName: "Concert Hall",
			SourceURL:       &sourceURL,
			AdminArea:       new("JP-13"),
		}
		d.pending(sc)
		d.createsVenue(nil, "Concert Hall", "JP-13")
		d.slotFree()
		d.inserts()
		// The concert candidate is deleted; no Series.Create is expected, since
		// approval never mints a series.
		d.deletes(sc.ID)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, nil, nil, "")
		require.NoError(t, err)

		// Event inserted under the pre-existing tour series.
		require.Len(t, d.createdConcerts, 1)
		assert.Equal(t, tourSeriesID, d.createdConcerts[0].SeriesID,
			"approved event must be inserted under the concert candidate's existing series ID")
	})
}

// newCreationUC returns a creation harness in which every listed venue
// resolves to a place of its own name, stored under neither its place id nor
// its name, so its venue is created; it expects one series to be looked up,
// minted, and swept at the end of the batch.
func newCreationUC(t *testing.T, venues ...string) *creationHarness {
	t.Helper()
	h := newCreationHarness(t)
	h.findsNoArtistEvents(1)
	h.createsSeries(1)
	for _, v := range venues {
		h.resolvesPlace(v, &entity.VenuePlace{ProviderUserID: "place-" + v, Name: v})
		h.createsVenue("place-"+v, v)
	}
	return h
}

// TestCreateFromDiscovered_OneCreatePerSeries shows that the events of a
// series to publish are stored together, in one Concert.Create call.
func TestCreateFromDiscovered_OneCreatePerSeries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	start := todAt(10, 0)
	tour := func(events ...*entity.DiscoveredEvent) entity.ConcertDiscoveredData {
		return entity.ConcertDiscoveredData{ArtistID: "artist-1", Series: []*entity.DiscoveredSeries{{
			Title: "Tour", Type: entity.SeriesTypeTour, SourceURL: "https://example.com/tour", Events: events,
		}}}
	}

	// @spec components/usecase/concert/create-from-discovered "Three-stop tour"
	t.Run("three events of a tour are created in one call", func(t *testing.T) {
		t.Parallel()
		h := newCreationUC(t, "Venue A", "Venue B", "Venue C")
		h.slotsFree(3)
		h.publishes(3)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(ctx, tour(
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue A", LocalDate: day, StartTime: start},
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue B", LocalDate: day.AddDate(0, 0, 1), StartTime: start},
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue C", LocalDate: day.AddDate(0, 0, 2), StartTime: start},
		)))

		require.Len(t, h.createCalls, 1)
		assert.Len(t, h.createCalls[0], 3)
	})

	// @spec components/usecase/concert/create-from-discovered "Everything staged"
	t.Run("a series whose events are all staged creates nothing", func(t *testing.T) {
		t.Parallel()
		h := newCreationUC(t)
		h.cannotResolvePlace("Unknown A")
		h.cannotResolvePlace("Unknown B")
		h.stages(2)
		h.sweepsOrphans()

		// No Concert.Create is expected.
		require.NoError(t, h.uc.CreateFromDiscovered(ctx, tour(
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Unknown A", LocalDate: day},
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Unknown B", LocalDate: day.AddDate(0, 0, 1)},
		)))

		assert.Len(t, h.staged, 2)
	})

	// @spec components/usecase/concert/create-from-discovered "Concerts cannot be created"
	t.Run("a failed create fails the batch", func(t *testing.T) {
		t.Parallel()
		h := newCreationUC(t, "Venue A")
		h.slotsFree(1)
		h.concert.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, apperr.New(codes.Unavailable, "database down")).Once()
		h.sweepsOrphans()

		err := h.uc.CreateFromDiscovered(ctx, tour(&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue A", LocalDate: day, StartTime: start}))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("an unknown start colliding with a known start decided in the same series is staged", func(t *testing.T) {
		t.Parallel()
		h := newCreationUC(t, "Venue A")
		// The second event, at the same place (Google Places is asked once),
		// finds the venue the first event created.
		h.venue.EXPECT().GetByPlaceID(mock.Anything, "place-Venue A").
			RunAndReturn(func(context.Context, string) (*entity.Venue, error) {
				require.Len(t, h.createdVenues, 1)
				return h.createdVenues[0], nil
			}).Once()
		h.slotsFree(2)
		h.stages(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(ctx, tour(
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue A", LocalDate: day, StartTime: start},
			&entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue A", LocalDate: day},
		)))

		require.Len(t, h.createCalls, 1)
		assert.Len(t, h.createCalls[0], 1)
		assert.Len(t, h.staged, 1)
	})

	// @spec components/usecase/concert/create-from-discovered "Search keeps new concerts"
	t.Run("the workflow processes both series of the search", func(t *testing.T) {
		t.Parallel()
		h := newCreationHarness(t)
		h.findsNoArtistEvents(2)
		h.createsSeries(2)
		for _, v := range []string{"Venue A", "Venue B"} {
			h.resolvesPlace(v, &entity.VenuePlace{ProviderUserID: "place-" + v, Name: v})
			h.createsVenue("place-"+v, v)
		}
		h.slotsFree(2)
		h.publishes(1, 1)
		h.sweepsOrphans()
		data := entity.ConcertDiscoveredData{ArtistID: "artist-1", Series: []*entity.DiscoveredSeries{
			{Title: "Tour", Type: entity.SeriesTypeTour, Events: []*entity.DiscoveredEvent{{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue A", LocalDate: day, StartTime: start}}},
			{Title: "Show", Type: entity.SeriesTypeSingle, Events: []*entity.DiscoveredEvent{{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Venue B", LocalDate: day.AddDate(0, 1, 0), StartTime: start}}},
		}}

		_, err := h.uc.RunCreateFromDiscovered(ctx, &inlineFlow{}, data)

		require.NoError(t, err)
		assert.Len(t, h.createCalls, 2, "one Concert.Create per series")
	})
}

// TestConcertCandidateUseCase_Approve_FillOnly approves a ConcertCandidate
// that brings a start time to the only Event at its Venue and date, which has
// none: the Event's times are filled and nothing is added.
func TestConcertCandidateUseCase_Approve_FillOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	artist := &entity.Artist{ID: "artist-fill", Name: "Fill Artist", MBID: "33333333-3333-3333-3333-333333333333"}
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	evening := todAt(18, 0)

	// build expects the candidate to resolve to the stored venue-fill, whose
	// only Event on the day is its Artist's and has no start, and to be
	// deleted afterwards. No
	// Concert.Create is expected: no Event is added.
	build := func(t *testing.T) (*approvalTestDeps, *entity.ConcertCandidate) {
		t.Helper()
		d := newApprovalTestDeps(t, artist)
		placeID, listed := "place-fill", "Fill Hall"
		sc := &entity.ConcertCandidate{
			ID: "candidate-fill", ArtistID: artist.ID, SeriesID: "series-staged", Title: "Show",
			LocalDate: day, StartTime: evening, ListedVenueName: listed,
			AdminArea: new("JP-13"), ResolvedPlaceID: &placeID,
		}
		d.pending(sc)
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, placeID).
			Return(&entity.Venue{ID: "venue-fill", Name: listed, GooglePlaceID: &placeID, ListedVenueName: &listed}, nil).Once()
		d.concertRepo.EXPECT().FindEventsByVenueAndDate(mock.Anything, []string{"venue-fill"}, []time.Time{day}).
			Return(foundAs([]*entity.Event{{ID: "event-unknown-start", VenueID: "venue-fill", LocalDate: day}}, artist.ID), nil).Once()
		d.findsArtistEvents()
		d.deletes(sc.ID)
		return d, sc
	}

	// @spec components/usecase/concert-candidate/approve "Fill only"
	t.Run("fills the Event's start time, adds nothing and removes the candidate", func(t *testing.T) {
		t.Parallel()
		d, sc := build(t)
		var filledIDs []string
		var filledStarts []*entity.TimeOfDay
		d.concertRepo.EXPECT().FillEventStartTimes(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, ids []string, starts, _ []*entity.TimeOfDay) error {
				filledIDs = append(filledIDs, ids...)
				filledStarts = append(filledStarts, starts...)
				return nil
			}).Once()
		d.seriesRepo.EXPECT().DeleteOrphaned(mock.Anything, mock.Anything).Return(0, nil).Once()

		_, err := d.uc.Approve(ctx, sc.ID, usecase.ApproveResolutionUnspecified, nil, nil, "")

		require.NoError(t, err)
		assert.Equal(t, []string{"event-unknown-start"}, filledIDs)
		require.Len(t, filledStarts, 1)
		assert.Equal(t, evening, filledStarts[0])
	})

	// @spec components/usecase/concert-candidate/approve "Last staged row of an empty series"
	t.Run("asks to remove the candidate's Series once nothing was added", func(t *testing.T) {
		t.Parallel()
		d, sc := build(t)
		d.concertRepo.EXPECT().FillEventStartTimes(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		// Series.DeleteOrphaned removes it when it has no Event and no
		// ConcertCandidate left.
		d.seriesRepo.EXPECT().DeleteOrphaned(mock.Anything, []string{"series-staged"}).Return(0, nil).Once()

		_, err := d.uc.Approve(ctx, sc.ID, usecase.ApproveResolutionUnspecified, nil, nil, "")

		require.NoError(t, err)
	})
}

func TestConcertCreationUseCase_VenueAreaAndZone(t *testing.T) {
	t.Parallel()
	localDate := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	batch := func(ev *entity.DiscoveredEvent) entity.ConcertDiscoveredData {
		return entity.ConcertDiscoveredData{ArtistID: "artist-1", Series: []*entity.DiscoveredSeries{{
			Title: "Show", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/s",
			Events: []*entity.DiscoveredEvent{ev},
		}}}
	}

	t.Run("New venue takes the listed name", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "New venue takes the listed name"
		// @spec components/entity/venue "Discovered venue with a resolved place"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		coords := &entity.Coordinates{Latitude: 35.55, Longitude: 139.78}
		h.resolvesPlace("Zepp Haneda(TOKYO)", &entity.VenuePlace{ProviderUserID: "place-h", Name: "Zepp Haneda (TOKYO)", Coordinates: coords})
		h.createsVenue("place-h", "Zepp Haneda(TOKYO)")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		before := time.Now()
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), batch(&entity.DiscoveredEvent{
			AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Zepp Haneda(TOKYO)", LocalDate: localDate,
		})))

		require.Len(t, h.createdVenues, 1)
		v := h.createdVenues[0]
		assert.Equal(t, "Zepp Haneda(TOKYO)", v.Name, "the map catalog's name is not stored")
		assert.Equal(t, "Zepp Haneda(TOKYO)", *v.ListedVenueName)
		assert.Equal(t, "JP-13", *v.AdminArea)
		assert.Equal(t, "Asia/Tokyo", *v.TimeZone)
		assert.Equal(t, coords, v.Coordinates)
		require.NotNil(t, v.CoordinatesFetchTime)
		assert.False(t, v.CoordinatesFetchTime.Before(before), "the coordinates carry when they were read")
	})

	t.Run("Known place", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Known place"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.resolvesPlace("Zepp Haneda", &entity.VenuePlace{ProviderUserID: "place-known", Name: "Zepp Haneda (TOKYO)"})
		h.storedVenue("place-known", "venue-known")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), batch(&entity.DiscoveredEvent{
			AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Zepp Haneda", LocalDate: localDate,
		})))

		assert.Empty(t, h.createdVenues, "no Venue is created")
		require.Len(t, h.createdConcerts, 1)
		assert.Equal(t, "venue-known", h.createdConcerts[0].VenueID)
	})

	t.Run("Place id differs from the stored venue", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Place id differs from the stored venue"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.resolvesPlace("Zepp Haneda", &entity.VenuePlace{ProviderUserID: "place-new", Name: "Zepp Haneda (TOKYO)"})
		h.venue.EXPECT().GetByPlaceID(mock.Anything, "place-new").Return(nil, notFound("venue")).Once()
		h.venue.EXPECT().GetByListedName(mock.Anything, "Zepp Haneda", "JP-13").
			Return(&entity.Venue{ID: "venue-listed"}, nil).Once()
		h.venue.EXPECT().BackfillPlaceID(mock.Anything, "venue-listed", "place-new").Return(nil).Once()
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), batch(&entity.DiscoveredEvent{
			AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Zepp Haneda", LocalDate: localDate,
		})))

		assert.Empty(t, h.createdVenues)
		require.Len(t, h.createdConcerts, 1)
		assert.Equal(t, "venue-listed", h.createdConcerts[0].VenueID)
	})

	t.Run("Catalog unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Catalog unavailable"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		// The venue is looked up before the Series is chosen, so the failure
		// comes before any Series is created.
		h.place.EXPECT().SearchPlace(mock.Anything, "Zepp Haneda", "JP-13").
			Return(nil, apperr.New(codes.Unavailable, "maps down")).Once()
		h.series.EXPECT().DeleteOrphaned(mock.Anything, mock.Anything).Return(0, nil).Maybe()

		err := h.uc.CreateFromDiscovered(context.Background(), batch(&entity.DiscoveredEvent{
			AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Zepp Haneda", LocalDate: localDate,
		}))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, h.createdConcerts)
	})

	t.Run("New overseas venue", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "New overseas venue"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.place.EXPECT().SearchPlace(mock.Anything, "Arkéa Arena", "FR-NAQ").
			Return([]*entity.VenuePlace{{ProviderUserID: "place-b", Name: "Arkéa Arena", CountryCode: "FR", AdminArea: new("FR-NAQ")}}, nil).Once()
		h.venue.EXPECT().GetByPlaceID(mock.Anything, "place-b").Return(nil, notFound("venue")).Once()
		h.venue.EXPECT().GetByListedName(mock.Anything, "Arkéa Arena", "FR-NAQ").Return(nil, notFound("venue")).Once()
		h.venue.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, v *entity.Venue) (string, error) {
			h.createdVenues = append(h.createdVenues, v)
			return v.ID, nil
		}).Once()
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), batch(&entity.DiscoveredEvent{
			AdminArea: new("FR-NAQ"), TimeZone: new("Europe/Paris"), ListedVenueName: "Arkéa Arena", LocalDate: localDate,
		})))

		assert.Equal(t, "FR-NAQ", *h.createdVenues[0].AdminArea)
		assert.Equal(t, "Europe/Paris", *h.createdVenues[0].TimeZone)
	})

	t.Run("Only places elsewhere", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Only places elsewhere"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		osaka := "JP-27"
		h.place.EXPECT().SearchPlace(mock.Anything, "The Independent", "US-CA").
			Return([]*entity.VenuePlace{{ProviderUserID: "place-osaka", Name: "The Independent", CountryCode: "JP", AdminArea: &osaka}}, nil).Once()
		h.candidate.EXPECT().Upsert(mock.Anything, mock.MatchedBy(func(c *entity.ConcertCandidate) bool {
			return c.ResolvedPlaceID == nil && c.ListedVenueName == "The Independent"
		})).Return(nil).Once()
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), batch(&entity.DiscoveredEvent{
			AdminArea: new("US-CA"), TimeZone: new("America/Los_Angeles"), ListedVenueName: "The Independent", LocalDate: localDate,
		})))
		assert.Empty(t, h.createdVenues)
	})

	t.Run("Area unknown", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Area unknown"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.stages(1)
		h.sweepsOrphans()

		// No place search and no venue: SearchPlace and Venue.Create are not expected.
		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), batch(&entity.DiscoveredEvent{
			ListedVenueName: "Hall", LocalDate: localDate,
		})))

		require.Len(t, h.staged, 1)
		assert.Nil(t, h.staged[0].AdminArea)
		assert.Nil(t, h.staged[0].TimeZone)
		assert.Nil(t, h.staged[0].ResolvedPlaceID)
	})

	t.Run("Time zone unknown", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Time zone unknown"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), batch(&entity.DiscoveredEvent{
			AdminArea: new("US-TX"), ListedVenueName: "Stubb's", LocalDate: localDate,
		})))

		require.Len(t, h.staged, 1)
		assert.Equal(t, "US-TX", *h.staged[0].AdminArea)
		assert.Nil(t, h.staged[0].TimeZone)
		assert.Nil(t, h.staged[0].ResolvedPlaceID)
	})

	t.Run("Venue not found", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Venue not found"
		h := newCreationHarness(t)
		h.findsNoArtistEvents(1)
		h.createsSeries(1)
		h.cannotResolvePlace("Hall")
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), batch(&entity.DiscoveredEvent{
			AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: "Hall", LocalDate: localDate,
		})))

		require.Len(t, h.staged, 1)
		assert.Nil(t, h.staged[0].ResolvedPlaceID)
	})
}

// foundAs wraps events as the Concerts a find-events lookup returns,
// performed by the given artists.
func foundAs(events []*entity.Event, artistIDs ...string) []*entity.Concert {
	concerts := make([]*entity.Concert, len(events))
	for i, e := range events {
		concerts[i] = &entity.Concert{Event: *e}
		for _, a := range artistIDs {
			concerts[i].Artists = append(concerts[i].Artists, &entity.Artist{ID: a})
		}
	}
	return concerts
}
