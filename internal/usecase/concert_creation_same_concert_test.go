package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// concertAt returns a stored event as a find-events lookup returns it.
func concertAt(e entity.Event, artistIDs ...string) *entity.Concert {
	c := &entity.Concert{Event: e}
	for _, a := range artistIDs {
		c.Artists = append(c.Artists, &entity.Artist{ID: a})
	}
	return c
}

// published is the publish state PUBLISHED, for an Organizer's event.
func published() *entity.PublishState {
	s := entity.PublishStatePublished
	return &s
}

// findsArtistEvents expects the lookup of the Artist's events on the
// series' dates to find concerts.
func (h *creationHarness) findsArtistEvents(artistID string, concerts ...*entity.Concert) {
	h.concert.EXPECT().FindEventsByArtistAndDate(mock.Anything, artistID, mock.Anything).Return(concerts, nil).Once()
}

// venueHolds expects the lookup at venueID on date to find concerts.
func (h *creationHarness) venueHolds(venueID string, date time.Time, concerts ...*entity.Concert) {
	h.concert.EXPECT().FindEventsByVenueAndDate(mock.Anything, []string{venueID}, []time.Time{date}).Return(concerts, nil).Once()
}

// resolvesTo expects the listed venue name to be looked up and to resolve to
// the stored venue venueID.
func (h *creationHarness) resolvesTo(name, venueID string) {
	placeID := "place-" + venueID
	h.resolvesPlace(name, &entity.VenuePlace{ProviderUserID: placeID, Name: name})
	h.storedVenue(placeID, venueID)
}

// sweeps expects the end-of-batch sweep and records the series it is given.
func (h *creationHarness) sweeps(into *[]string) {
	h.series.EXPECT().DeleteOrphaned(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ids []string) (int64, error) {
			*into = append(*into, ids...)
			return 0, nil
		}).Once()
}

// found returns a discovered event in Tokyo.
func found(name string, date time.Time, start *entity.TimeOfDay) *entity.DiscoveredEvent {
	return &entity.DiscoveredEvent{AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), ListedVenueName: name, LocalDate: date, StartTime: start}
}

// discovered returns a batch of one series of the Artist.
func discovered(artistID, title string, typ entity.SeriesType, events ...*entity.DiscoveredEvent) entity.ConcertDiscoveredData {
	return entity.ConcertDiscoveredData{ArtistID: artistID, Series: []*entity.DiscoveredSeries{{
		Title: title, Type: typ, SourceURL: "https://example.com/" + title, Events: events,
	}}}
}

func TestCreateFromDiscovered_SeriesChoice(t *testing.T) {
	t.Parallel()

	const a, b = "artist-a", "artist-b"
	dec19 := time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC)
	dec20 := time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)
	at17 := todAt(17, 0)

	t.Run("Same venue under another name joins", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Same venue under another name joins"
		h := newCreationHarness(t)
		kyocera := concertAt(entity.Event{ID: "event-k", SeriesID: "series-k", VenueID: "venue-v", LocalDate: dec19, ListedVenueName: new("Kyocera Dome Osaka")}, a)
		h.findsArtistEvents(a, kyocera)
		h.resolvesTo("京セラドーム大阪", "venue-v")
		h.resolvesTo("Hall W", "venue-w")
		h.venueHolds("venue-v", dec19, kyocera)
		h.slotsFree(1)
		h.concert.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-k"}, mock.Anything, mock.Anything).Return(nil).Once()
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Dome Tour", entity.SeriesTypeTour,
			found("京セラドーム大阪", dec19, at17), found("Hall W", dec20, at17))))

		assert.Empty(t, h.createdSeries, "no Series is created")
		require.Len(t, h.createdConcerts, 1)
		assert.Equal(t, "series-k", h.createdConcerts[0].SeriesID, "the tour's events go under that Event's Series")
	})

	t.Run("Dates announced later join their tour", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Dates announced later join their tour"
		h := newCreationHarness(t)
		h.upcomingTours = []*entity.Series{{ID: "series-p", Title: "2026 LE SSERAFIM TOUR 'PUREFLOW'", Type: entity.SeriesTypeTour}}
		h.findsArtistEvents(a)
		h.resolvesTo("Hall W", "venue-w")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "2026 LE SSERAFIM TOUR “PUREFLOW”", entity.SeriesTypeTour,
			found("Hall W", dec20, at17))))

		assert.Empty(t, h.createdSeries, "no Series is created")
		require.Len(t, h.createdConcerts, 1)
		assert.Equal(t, "series-p", h.createdConcerts[0].SeriesID)
	})

	t.Run("Tour already over is not joined", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Tour already over is not joined"
		// Series.ListUpcomingToursByArtist leaves out a tour whose events are
		// all before the current date, so the Artist has no upcoming tour.
		h := newCreationHarness(t)
		h.findsArtistEvents(a)
		h.createsSeries(1)
		h.resolvesTo("Hall W", "venue-w")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "2026 LE SSERAFIM TOUR 'PUREFLOW'", entity.SeriesTypeTour,
			found("Hall W", dec20, at17))))

		require.Len(t, h.createdSeries, 1, "a new TOUR Series is created")
		assert.Equal(t, entity.SeriesTypeTour, h.createdSeries[0].Type)
		assert.Equal(t, h.createdSeries[0].ID, h.createdConcerts[0].SeriesID)
	})

	t.Run("Shared bill does not pull the tour into another Artist's Series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Shared bill does not pull the tour into another Artist's Series"
		h := newCreationHarness(t)
		shared := concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec19, StartTime: at17, ListedVenueName: new("Zepp Haneda")}, a, b)
		h.findsArtistEvents(a, shared)
		h.createsSeries(1)
		h.venueHolds("venue-v", dec19, shared)
		h.resolvesTo("Hall W", "venue-w")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Tour", entity.SeriesTypeTour,
			found("Zepp Haneda", dec19, at17), found("Hall W", dec20, at17))))

		require.Len(t, h.createdSeries, 1, "the tour gets a Series of its own")
		require.Len(t, h.createdConcerts, 1)
		assert.NotEqual(t, "series-b", h.createdConcerts[0].SeriesID)
	})

	t.Run("an Organizer's event at the same venue does not pull the series in", func(t *testing.T) {
		t.Parallel()
		h := newCreationHarness(t)
		organizers := concertAt(entity.Event{ID: "event-o", SeriesID: "series-o", VenueID: "venue-v", LocalDate: dec19, StartTime: todAt(13, 0),
			ListedVenueName: new("Hall V"), PublishState: published()}, a)
		h.findsArtistEvents(a, organizers)
		h.createsSeries(1)
		h.resolvesTo("ホールV", "venue-v")
		h.venueHolds("venue-v", dec19, organizers)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Evening", entity.SeriesTypeSingle, found("ホールV", dec19, todAt(19, 0)))))

		require.Len(t, h.createdConcerts, 1)
		assert.NotEqual(t, "series-o", h.createdConcerts[0].SeriesID, "a discovered event never goes under an Organizer's Series")
	})

	t.Run("Standalone show is not joined by title", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Standalone show is not joined by title"
		h := newCreationHarness(t)
		h.upcomingTours = []*entity.Series{{ID: "series-live", Title: "Live", Type: entity.SeriesTypeTour}}
		h.findsArtistEvents(a)
		h.createsSeries(1)
		h.resolvesTo("Hall W", "venue-w")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Live", entity.SeriesTypeSingle,
			found("Hall W", dec20, at17))))

		require.Len(t, h.createdSeries, 1, "a new SINGLE Series is created")
		assert.Equal(t, entity.SeriesTypeSingle, h.createdSeries[0].Type)
		h.series.AssertNotCalled(t, "ListUpcomingToursByArtist", mock.Anything, mock.Anything, mock.Anything)
	})
}

func TestCreateFromDiscovered_ArtistsOwnEventUnderAnotherName(t *testing.T) {
	t.Parallel()

	const a = "artist-a"
	dec19 := time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC)
	at17, at18 := todAt(17, 0), todAt(18, 0)

	t.Run("Start time announced under another name", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Start time announced under another name"
		h := newCreationHarness(t)
		own := concertAt(entity.Event{ID: "event-k", SeriesID: "series-k", VenueID: "venue-v", LocalDate: dec19, ListedVenueName: new("Kyocera Dome Osaka")}, a)
		h.findsArtistEvents(a, own)
		h.resolvesTo("京セラドーム大阪", "venue-v")
		h.venueHolds("venue-v", dec19, own)
		h.concert.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-k"}, []*entity.TimeOfDay{at17}, []*entity.TimeOfDay{nil}).Return(nil).Once()
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Dome", entity.SeriesTypeSingle, found("京セラドーム大阪", dec19, at17))))

		assert.Empty(t, h.createdConcerts, "no new Event is stored")
		assert.Empty(t, h.staged)
	})

	t.Run("Same show under another name", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Same show under another name"
		h := newCreationHarness(t)
		own := concertAt(entity.Event{ID: "event-k", SeriesID: "series-k", VenueID: "venue-v", LocalDate: dec19, StartTime: at17, ListedVenueName: new("京セラドーム大阪")}, a)
		h.findsArtistEvents(a, own)
		h.resolvesTo("Kyocera Dome Osaka", "venue-v")
		h.venueHolds("venue-v", dec19, own)
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Dome", entity.SeriesTypeSingle, found("Kyocera Dome Osaka", dec19, at17))))

		require.Len(t, h.staged, 1)
		assert.Equal(t, "series-k", h.staged[0].SeriesID, "staged under the Series of the Event at the same Venue")
		assert.Empty(t, h.createdConcerts, "nothing is published")
	})

	t.Run("Start time unknown in a later listing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Start time unknown in a later listing"
		h := newCreationHarness(t)
		own := concertAt(entity.Event{ID: "event-z", SeriesID: "series-z", VenueID: "venue-v", LocalDate: dec19, StartTime: at18, ListedVenueName: new("Zepp Haneda(TOKYO)")}, a)
		h.findsArtistEvents(a, own)
		h.venueHolds("venue-v", dec19, own)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Zepp", entity.SeriesTypeSingle, found("Zepp Haneda(TOKYO)", dec19, nil))))

		assert.Empty(t, h.createdConcerts)
		assert.Empty(t, h.staged)
	})

	t.Run("Start time unknown under another name", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Start time unknown under another name"
		h := newCreationHarness(t)
		own := concertAt(entity.Event{ID: "event-k", SeriesID: "series-k", VenueID: "venue-v", LocalDate: dec19, StartTime: at17, ListedVenueName: new("京セラドーム大阪")}, a)
		h.findsArtistEvents(a, own)
		h.resolvesTo("Kyocera Dome Osaka", "venue-v")
		h.venueHolds("venue-v", dec19, own)
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Dome", entity.SeriesTypeSingle, found("Kyocera Dome Osaka", dec19, nil))))

		require.Len(t, h.staged, 1)
		assert.Empty(t, h.createdConcerts)
	})

	t.Run("Name kept from an earlier review", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Name kept from an earlier review"
		h := newCreationHarness(t)
		own := concertAt(entity.Event{ID: "event-k", SeriesID: "series-k", VenueID: "venue-v", LocalDate: dec19, StartTime: at17,
			ListedVenueName: new("京セラドーム大阪"), OtherListedVenueNames: []string{"Kyocera Dome Osaka"}}, a)
		h.findsArtistEvents(a, own)
		// No Places or venue expectation: looking the venue up fails the mock.
		h.venueHolds("venue-v", dec19, own)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Dome", entity.SeriesTypeSingle, found("Kyocera Dome Osaka", dec19, at17))))

		assert.Empty(t, h.createdConcerts)
		assert.Empty(t, h.staged)
	})

	t.Run("the 2026-11-20 shape neither fails nor adds an event", func(t *testing.T) {
		t.Parallel()
		// The Artist has an unknown-start Event and a 17:00 Event under
		// another name at one Venue; the found 17:00 event under the first
		// name used to be filled onto the unknown-start Event, taking the
		// other's slot and failing the whole batch.
		h := newCreationHarness(t)
		unknown := concertAt(entity.Event{ID: "event-u", SeriesID: "series-k", VenueID: "venue-v", LocalDate: dec19, ListedVenueName: new("Kyocera Dome Osaka")}, a)
		timed := concertAt(entity.Event{ID: "event-t", SeriesID: "series-k", VenueID: "venue-v", LocalDate: dec19, StartTime: at17, ListedVenueName: new("京セラドーム大阪")}, a)
		h.findsArtistEvents(a, unknown, timed)
		h.venueHolds("venue-v", dec19, unknown, timed)
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Dome", entity.SeriesTypeSingle, found("Kyocera Dome Osaka", dec19, at17))))

		assert.Empty(t, h.createdConcerts, "no Event is added")
		require.Len(t, h.staged, 1, "the event is staged for an admin")
	})
}

func TestCreateFromDiscovered_AnotherArtistsEvent(t *testing.T) {
	t.Parallel()

	const a, b = "artist-a", "artist-b"
	dec22 := time.Date(2026, 12, 22, 0, 0, 0, 0, time.UTC)
	dec23 := time.Date(2026, 12, 23, 0, 0, 0, 0, time.UTC)
	at18 := todAt(18, 0)

	t.Run("Shared bill under the same name", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Shared bill under the same name"
		h := newCreationHarness(t)
		h.findsArtistEvents(a)
		h.createsSeries(1)
		h.resolvesTo("Zepp Haneda (TOKYO)", "venue-v")
		h.venueHolds("venue-v", dec22, concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec22, StartTime: at18, ListedVenueName: new("Zepp Haneda(TOKYO)")}, b))
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Live", entity.SeriesTypeSingle, found("Zepp Haneda (TOKYO)", dec22, at18))))

		// Concert.Create keeps B's Event at that slot, adds A to its
		// performers and tells A's followers (CONCERT.created).
		require.Len(t, h.createdConcerts, 1)
		c := h.createdConcerts[0]
		assert.Equal(t, "venue-v", c.VenueID)
		assert.Equal(t, at18, c.StartTime)
		assert.Equal(t, "series-b", c.SeriesID, "stored in B's Event's slot, under its Series")
		assert.Equal(t, []string{a}, c.ArtistIDs())
	})

	t.Run("Organizer's event at the same slot", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Organizer's event at the same slot"
		h := newCreationHarness(t)
		h.findsArtistEvents(a)
		h.createsSeries(1)
		h.resolvesTo("Zepp Haneda(TOKYO)", "venue-v")
		h.venueHolds("venue-v", dec22, concertAt(entity.Event{ID: "event-o", SeriesID: "series-o", VenueID: "venue-v", LocalDate: dec22, StartTime: at18,
			ListedVenueName: new("Zepp Haneda(TOKYO)"), PublishState: published()}, b))
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Live", entity.SeriesTypeSingle, found("Zepp Haneda(TOKYO)", dec22, at18))))

		require.Len(t, h.staged, 1)
		assert.Empty(t, h.createdConcerts, "the Organizer's Event is unchanged")
	})

	t.Run("Shared bill without a start time", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Shared bill without a start time"
		h := newCreationHarness(t)
		h.findsArtistEvents(a)
		h.createsSeries(1)
		h.resolvesTo("Zepp Haneda(TOKYO)", "venue-v")
		h.venueHolds("venue-v", dec22, concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec22, ListedVenueName: new("Zepp Haneda(TOKYO)")}, b))
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Live", entity.SeriesTypeSingle, found("Zepp Haneda(TOKYO)", dec22, nil))))

		require.Len(t, h.staged, 1)
		assert.Empty(t, h.createdConcerts)
	})

	t.Run("New date and a shared bill in one series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "New date and a shared bill in one series"
		h := newCreationHarness(t)
		h.findsArtistEvents(a)
		h.createsSeries(1)
		h.resolvesTo("Zepp Haneda(TOKYO)", "venue-v")
		h.resolvesTo("Hall W", "venue-w")
		h.venueHolds("venue-v", dec22, concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec22, StartTime: at18, ListedVenueName: new("Zepp Haneda(TOKYO)")}, b))
		h.slotsFree(1)
		h.publishes(2)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Tour", entity.SeriesTypeTour,
			found("Hall W", dec23, at18), found("Zepp Haneda(TOKYO)", dec22, at18))))

		require.Len(t, h.createCalls, 1, "Concert.Create is called once")
		require.Len(t, h.createCalls[0], 2)
		assert.Equal(t, h.createdSeries[0].ID, h.createCalls[0][0].SeriesID, "the new date is under the tour's Series")
		assert.Equal(t, "series-b", h.createCalls[0][1].SeriesID, "the shared bill is under the other Artist's Event's Series")
	})
}

func TestCreateFromDiscovered_SameArtistAtAnotherVenue(t *testing.T) {
	t.Parallel()

	const a = "artist-a"
	aug28 := time.Date(2027, 8, 28, 0, 0, 0, 0, time.UTC)

	t.Run("Renamed hall", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Renamed hall"
		h := newCreationHarness(t)
		gaishi := concertAt(entity.Event{ID: "event-g", SeriesID: "series-g", VenueID: "venue-gaishi", LocalDate: aug28, StartTime: todAt(18, 0), ListedVenueName: new("日本ガイシホール")}, a)
		h.findsArtistEvents(a, gaishi)
		h.createsSeries(1)
		h.resolvesTo("クロコくんホール (旧 日本ガイシホール)", "venue-kuroko")
		h.slotsFree(1)
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Hall", entity.SeriesTypeSingle,
			found("クロコくんホール (旧 日本ガイシホール)", aug28, todAt(18, 0)))))

		require.Len(t, h.staged, 1)
		assert.Empty(t, h.createdConcerts, "nothing is published")
	})

	t.Run("Two shows of one day at two venues", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Two shows of one day at two venues"
		dec6 := time.Date(2026, 12, 6, 0, 0, 0, 0, time.UTC)
		h := newCreationHarness(t)
		h.findsArtistEvents(a, concertAt(entity.Event{ID: "event-m", SeriesID: "series-m", VenueID: "venue-m", LocalDate: dec6, StartTime: todAt(13, 0), ListedVenueName: new("Matinee Hall")}, a))
		h.createsSeries(1)
		h.resolvesTo("Evening Hall", "venue-e")
		h.slotsFree(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Evening", entity.SeriesTypeSingle,
			found("Evening Hall", dec6, todAt(19, 0)))))

		assert.Empty(t, h.staged, "not staged for this reason")
		require.Len(t, h.createdConcerts, 1)
	})
}

func TestCreateFromDiscovered_StartTimeHeldByAnotherEvent(t *testing.T) {
	t.Parallel()

	const a, b = "artist-a", "artist-b"
	dec31 := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	jan2 := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	at18 := todAt(18, 0)
	zepp := "Zepp Haneda(TOKYO)"
	ownUnknown := func() *entity.Concert {
		return concertAt(entity.Event{ID: "event-a", SeriesID: "series-a", VenueID: "venue-v", LocalDate: dec31, ListedVenueName: &zepp}, a)
	}

	t.Run("Co-headliner's time already known", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Co-headliner's time already known"
		// @spec components/usecase/concert/create-from-discovered "Series of a merged event left empty"
		h := newCreationHarness(t)
		own := ownUnknown()
		h.findsArtistEvents(a, own)
		h.venueHolds("venue-v", dec31, own, concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec31, StartTime: at18, ListedVenueName: new("ZEPP HANEDA (TOKYO)")}, b))
		h.concert.EXPECT().Merge(mock.Anything, "event-a", "event-b").Return(nil).Once()
		h.resolvesTo("Hall W", "venue-w")
		h.slotsFree(1)
		h.publishes(1)
		var swept []string
		h.sweeps(&swept)

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Countdown", entity.SeriesTypeTour,
			found(zepp, dec31, at18), found("Hall W", jan2, at18))))

		// A's followers are not told again: only the other event of the batch
		// is in Concert.Create, and Merge announces nothing.
		require.Len(t, h.createdConcerts, 1, "the batch's other events are processed")
		assert.Equal(t, "venue-w", h.createdConcerts[0].VenueID)
		assert.Contains(t, swept, "series-a", "the merged Event's Series is swept at the end of the batch")
	})

	t.Run("a merge that finds the event gone stages the event", func(t *testing.T) {
		t.Parallel()
		h := newCreationHarness(t)
		own := ownUnknown()
		h.findsArtistEvents(a, own)
		h.venueHolds("venue-v", dec31, own, concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec31, StartTime: at18, ListedVenueName: &zepp}, b))
		h.concert.EXPECT().Merge(mock.Anything, "event-a", "event-b").Return(apperr.New(codes.NotFound, "gone")).Once()
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Countdown", entity.SeriesTypeSingle, found(zepp, dec31, at18))),
			"the batch does not fail")
		require.Len(t, h.staged, 1)
	})

	t.Run("Tour with a ticket sale is not merged away", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Tour with a ticket sale is not merged away"
		h := newCreationHarness(t)
		own := ownUnknown()
		h.findsArtistEvents(a, own)
		h.venueHolds("venue-v", dec31, own, concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec31, StartTime: at18, ListedVenueName: &zepp}, b))
		// Concert.Merge refuses: A's Event is the last of a Series with a ticket sale.
		h.concert.EXPECT().Merge(mock.Anything, "event-a", "event-b").Return(apperr.New(codes.FailedPrecondition, "sells alone")).Once()
		h.stages(1)
		var swept []string
		h.sweeps(&swept)

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Countdown", entity.SeriesTypeSingle, found(zepp, dec31, at18))))

		require.Len(t, h.staged, 1, "A's event is staged")
		assert.NotContains(t, swept, "series-a", "A's Series is not swept")
	})

	t.Run("Held under another name", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Held under another name"
		h := newCreationHarness(t)
		own := ownUnknown()
		h.findsArtistEvents(a, own)
		h.venueHolds("venue-v", dec31, own, concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec31, StartTime: at18, ListedVenueName: new("羽田")}, b))
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Countdown", entity.SeriesTypeSingle, found(zepp, dec31, at18))))

		// No Merge or FillEventStartTimes expectation: A's Event is unchanged.
		require.Len(t, h.staged, 1)
	})

	t.Run("Held by an Organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Held by an Organizer's event"
		h := newCreationHarness(t)
		own := ownUnknown()
		h.findsArtistEvents(a, own)
		h.venueHolds("venue-v", dec31, own, concertAt(entity.Event{ID: "event-o", SeriesID: "series-o", VenueID: "venue-v", LocalDate: dec31, StartTime: at18,
			ListedVenueName: &zepp, PublishState: published()}, b))
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Countdown", entity.SeriesTypeSingle, found(zepp, dec31, at18))))

		// No Merge, FillEventStartTimes or Create expectation: both Events are unchanged.
		require.Len(t, h.staged, 1)
	})

	t.Run("Organizer's unknown-start event already listed under the name", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Organizer's unknown-start event already listed under the name"
		h := newCreationHarness(t)
		own := ownUnknown()
		own.ListedVenueName, own.PublishState = &zepp, published()
		h.findsArtistEvents(a, own)
		h.createsSeries(1)
		h.venueHolds("venue-v", dec31, own)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Countdown", entity.SeriesTypeSingle, found(zepp, dec31, at18))))

		// No FillEventStartTimes or Create expectation: the Organizer's Event is unchanged.
		require.Len(t, h.staged, 0)
	})

	t.Run("Organizer's unknown-start event under another name", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Organizer's unknown-start event under another name"
		h := newCreationHarness(t)
		own := ownUnknown()
		own.ListedVenueName, own.PublishState = new("羽田"), published()
		h.findsArtistEvents(a, own)
		h.createsSeries(1)
		h.resolvesTo(zepp, "venue-v")
		h.venueHolds("venue-v", dec31, own)
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Countdown", entity.SeriesTypeSingle, found(zepp, dec31, at18))))

		// No FillEventStartTimes or Create expectation: the Organizer's Event is unchanged.
		require.Len(t, h.staged, 1)
	})

	t.Run("Two start times beside an Organizer's unknown-start event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Two start times beside an Organizer's unknown-start event"
		h := newCreationHarness(t)
		own := ownUnknown()
		own.ListedVenueName, own.PublishState = &zepp, published()
		h.findsArtistEvents(a, own)
		h.createsSeries(1)
		h.venueHolds("venue-v", dec31, own)
		h.venueHolds("venue-v", dec31, own)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(),
			discovered(a, "Countdown", entity.SeriesTypeSingle, found(zepp, dec31, todAt(13, 0)), found(zepp, dec31, at18))))

		require.Len(t, h.createdConcerts, 1, "only the second start time is a new event")
		assert.Equal(t, at18, h.createdConcerts[0].StartTime)
		assert.Empty(t, h.staged)
	})

	t.Run("Series being written during a merge", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Series being written during a merge"
		h := newCreationHarness(t)
		own := ownUnknown()
		h.findsArtistEvents(a, own)
		h.venueHolds("venue-v", dec31, own, concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec31, StartTime: at18, ListedVenueName: &zepp}, b))
		h.concert.EXPECT().Merge(mock.Anything, "event-a", "event-b").Return(apperr.New(codes.Unavailable, "series locked")).Once()

		err := h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Countdown", entity.SeriesTypeSingle, found(zepp, dec31, at18)))

		// No Upsert expectation: nothing is staged, and the batch is retried.
		require.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, h.staged)
	})

	t.Run("Failed batch is swept", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Failed batch is swept"
		h := newCreationHarness(t)
		own := ownUnknown()
		later := concertAt(entity.Event{ID: "event-c", SeriesID: "series-c", VenueID: "venue-v", LocalDate: dec31.AddDate(0, 0, 1), ListedVenueName: &zepp}, a)
		h.findsArtistEvents(a, own, later)
		h.venueHolds("venue-v", dec31, own, concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-v", LocalDate: dec31, StartTime: at18, ListedVenueName: &zepp}, b))
		h.concert.EXPECT().Merge(mock.Anything, "event-a", "event-b").Return(nil).Once()
		// The batch is cut off, as by a worker shutdown.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h.concert.EXPECT().FindEventsByVenueAndDate(mock.Anything, []string{"venue-v"}, []time.Time{dec31.AddDate(0, 0, 1)}).
			RunAndReturn(func(context.Context, []string, []time.Time) ([]*entity.Concert, error) {
				cancel()
				return nil, apperr.New(codes.Unavailable, "database down")
			}).Once()
		var swept []string
		h.series.EXPECT().DeleteOrphaned(mock.Anything, mock.Anything).
			RunAndReturn(func(sweepCtx context.Context, ids []string) (int64, error) {
				require.NoError(t, sweepCtx.Err(), "the sweep outlives the batch")
				swept = append(swept, ids...)
				return 0, nil
			}).Once()

		err := h.uc.CreateFromDiscovered(ctx, discovered(a, "Countdown", entity.SeriesTypeSingle,
			found(zepp, dec31, at18), found(zepp, dec31.AddDate(0, 0, 1), at18)))

		require.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Contains(t, swept, own.SeriesID, "the merged-away event's Series is swept")
	})

	t.Run("Slot taken meanwhile", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Slot taken meanwhile"
		h := newCreationHarness(t)
		own := ownUnknown()
		h.findsArtistEvents(a, own)
		h.venueHolds("venue-v", dec31, own)
		h.concert.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-a"}, mock.Anything, mock.Anything).
			Return(apperr.New(codes.AlreadyExists, "slot taken")).Once()
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Countdown", entity.SeriesTypeSingle, found(zepp, dec31, at18))),
			"the batch does not fail")
		require.Len(t, h.staged, 1)
	})
}

func TestCreateFromDiscovered_SameTimeAtTwoVenuesInOneSeries(t *testing.T) {
	t.Parallel()
	const a = "artist-a"
	dec19 := time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC)
	at18 := todAt(18, 0)

	t.Run("a time filled earlier in the series stages the same time at another venue", func(t *testing.T) {
		t.Parallel()
		h := newCreationHarness(t)
		own := concertAt(entity.Event{ID: "event-a", SeriesID: "series-a", VenueID: "venue-v", LocalDate: dec19, ListedVenueName: new("Hall V")}, a)
		h.findsArtistEvents(a, own)
		h.resolvesTo("Hall W", "venue-w")
		h.venueHolds("venue-v", dec19, own)
		h.concert.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-a"}, mock.Anything, mock.Anything).Return(nil).Once()
		h.slotsFree(1)
		h.stages(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Tour", entity.SeriesTypeTour,
			found("Hall V", dec19, at18), found("Hall W", dec19, at18))))

		require.Len(t, h.staged, 1)
		assert.Equal(t, "Hall W", h.staged[0].ListedVenueName)
		assert.Empty(t, h.createdConcerts)
	})

	t.Run("a time planned earlier in the series stages the same time at another venue", func(t *testing.T) {
		t.Parallel()
		h := newCreationHarness(t)
		h.findsArtistEvents(a)
		h.createsSeries(1)
		h.resolvesTo("Hall V", "venue-v")
		h.resolvesTo("Hall W", "venue-w")
		h.slotsFree(2)
		h.stages(1)
		h.publishes(1)
		h.sweepsOrphans()

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered(a, "Tour", entity.SeriesTypeTour,
			found("Hall V", dec19, at18), found("Hall W", dec19, at18))))

		require.Len(t, h.createdConcerts, 1)
		assert.Equal(t, "venue-v", h.createdConcerts[0].VenueID)
		require.Len(t, h.staged, 1)
		assert.Equal(t, "Hall W", h.staged[0].ListedVenueName)
	})
}

func TestCreateFromDiscovered_EmptyNewSeries(t *testing.T) {
	t.Parallel()

	t.Run("Created series left empty", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/create-from-discovered "Created series left empty"
		h := newCreationHarness(t)
		h.findsArtistEvents("artist-a")
		h.createsSeries(1)
		h.cannotResolvePlace("Unknown Hall")
		h.stages(1)
		var swept []string
		h.sweeps(&swept)

		require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered("artist-a", "Live", entity.SeriesTypeSingle,
			found("Unknown Hall", time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), nil))))

		require.Len(t, h.createdSeries, 1)
		assert.Equal(t, []string{h.createdSeries[0].ID}, swept, "only the Series this batch created is looked at")
	})
}
