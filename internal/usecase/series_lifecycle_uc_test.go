package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	lifecycleSeriesID = "019a0000-0000-7000-8000-0000000000a1"
	lifecycleVenueID  = "019a0000-0000-7000-8000-0000000000b1"
	lifecyclePlaceID  = "ChIJ-zepp-haneda"
	lifecycleVenue    = "Zepp Haneda"
	lifecycleArtist2  = "019a0000-0000-7000-8000-0000000000c2"
)

// lifecycleSeries returns a first-party Series of the authoring organizer with
// the given visibility.
func lifecycleSeries(visibility entity.SeriesVisibility) *entity.Series {
	return &entity.Series{
		ID:          lifecycleSeriesID,
		Title:       "ARENA TOUR 2026",
		Type:        entity.SeriesTypeTour,
		OrganizerID: new(authoringOrgID),
		Visibility:  &visibility,
	}
}

// lifecycleEvent returns a stored Event of the series in the given state,
// at Zepp Haneda on date, as GetAuthored returns it.
func lifecycleEvent(id string, state entity.PublishState, date time.Time, start *time.Time) *entity.Event {
	return &entity.Event{
		ID:              id,
		SeriesID:        lifecycleSeriesID,
		VenueID:         lifecycleVenueID,
		Venue:           &entity.Venue{ID: lifecycleVenueID, Name: "Zepp Haneda (TOKYO)", GooglePlaceID: new(lifecyclePlaceID)},
		ListedVenueName: new(lifecycleVenue),
		LocalDate:       date,
		StartTime:       start,
		PublishState:    &state,
		PerAccountLimit: new(4),
	}
}

// inputFor returns the input that resends a stored event unchanged.
func inputFor(e *entity.Event) *usecase.DraftEventInput {
	return &usecase.DraftEventInput{
		EventID:         new(e.ID),
		VenueName:       *e.ListedVenueName,
		PlaceID:         e.Venue.GooglePlaceID,
		LocalDate:       e.LocalDate,
		StartTime:       e.StartTime,
		OpenTime:        e.OpenTime,
		PerAccountLimit: e.PerAccountLimit,
	}
}

// editOf returns the edit of the stored series as the editor sends it back.
func editOf(s *entity.Series) *entity.Series {
	v := *s.Visibility
	return &entity.Series{Title: s.Title, Type: s.Type, SourceURL: s.SourceURL, Visibility: &v}
}

func at(date time.Time, hour int) *time.Time {
	t := date.Add(time.Duration(hour-9) * time.Hour) // JST hour
	return &t
}

func representsArtists(d *authoringDeps, ids ...string) {
	artists := make([]*entity.Artist, len(ids))
	for i, id := range ids {
		artists[i] = &entity.Artist{ID: id}
	}
	d.organizerRepo.EXPECT().ListArtists(mock.Anything, authoringOrgID).Return(artists, nil).Maybe()
}

func TestSeriesUseCase_Create_Lifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("store one DRAFT event for a represented artist and announce nothing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/create "Draft for a represented artist"
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, lifecyclePlaceID).Return(&entity.Venue{ID: lifecycleVenueID}, nil).Once()
		var stored []*entity.Event
		d.seriesRepo.EXPECT().CreateDraft(mock.Anything, mock.Anything, mock.Anything, []string{authoringArtistID}).
			Run(func(_ context.Context, s *entity.Series, events []*entity.Event, _ []string) {
				assert.Equal(t, authoringOrgID, *s.OrganizerID)
				stored = events
			}).Return(nil).Once()
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, mock.Anything).Return(lifecycleSeries(entity.SeriesVisibilityPublic), nil, nil).Once()

		_, _, err := d.uc.Create(ctx, authoringOrgID, editOf(lifecycleSeries(entity.SeriesVisibilityPublic)),
			[]*usecase.DraftEventInput{{VenueName: lifecycleVenue, PlaceID: new(lifecyclePlaceID), LocalDate: futureDate()}},
			[]string{authoringArtistID})

		require.NoError(t, err)
		require.Len(t, stored, 1)
		assert.True(t, stored[0].HasPublishState(entity.PublishStateDraft))
		d.publisher.AssertNotCalled(t, "PublishEvent", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("store two DRAFT events for two showtimes on one date", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/create "Two showtimes"
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, lifecyclePlaceID).Return(&entity.Venue{ID: lifecycleVenueID}, nil).Twice()
		var stored []*entity.Event
		d.seriesRepo.EXPECT().CreateDraft(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Run(func(_ context.Context, _ *entity.Series, events []*entity.Event, _ []string) { stored = events }).
			Return(nil).Once()
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, mock.Anything).Return(lifecycleSeries(entity.SeriesVisibilityPublic), nil, nil).Once()

		date := futureDate()
		_, _, err := d.uc.Create(ctx, authoringOrgID, editOf(lifecycleSeries(entity.SeriesVisibilityPublic)), []*usecase.DraftEventInput{
			{VenueName: lifecycleVenue, PlaceID: new(lifecyclePlaceID), LocalDate: date, StartTime: at(date, 13)},
			{VenueName: lifecycleVenue, PlaceID: new(lifecyclePlaceID), LocalDate: date, StartTime: at(date, 18)},
		}, []string{authoringArtistID})

		require.NoError(t, err)
		require.Len(t, stored, 2)
		for _, e := range stored {
			assert.True(t, e.HasPublishState(entity.PublishStateDraft))
		}
		assert.NotEqual(t, stored[0].ID, stored[1].ID)
	})

	invalid := []struct {
		name   string
		inputs []*usecase.DraftEventInput
	}{
		{
			// @spec components/usecase/series/create "No event"
			name: "fail with INVALID_ARGUMENT when no event is given",
		},
		{
			// @spec components/usecase/series/create "Date in the past"
			name:   "fail with INVALID_ARGUMENT for a date before today",
			inputs: []*usecase.DraftEventInput{{VenueName: lifecycleVenue, PlaceID: new(lifecyclePlaceID), LocalDate: authoringNow.AddDate(0, 0, -1).Truncate(24 * time.Hour)}},
		},
		{
			// @spec components/usecase/series/create "Doors after start"
			name: "fail with INVALID_ARGUMENT when doors open after the start",
			inputs: []*usecase.DraftEventInput{{
				VenueName: lifecycleVenue, PlaceID: new(lifecyclePlaceID), LocalDate: futureDate(),
				StartTime: at(futureDate(), 18), OpenTime: at(futureDate(), 19),
			}},
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newAuthoringDeps(t)
			representsArtists(d, authoringArtistID)

			_, _, err := d.uc.Create(ctx, authoringOrgID, editOf(lifecycleSeries(entity.SeriesVisibilityPublic)), tt.inputs, []string{authoringArtistID})

			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		})
	}

	t.Run("fail with PERMISSION_DENIED for an artist the organizer does not represent", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/create "Unrepresented artist"
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)

		_, _, err := d.uc.Create(ctx, authoringOrgID, editOf(lifecycleSeries(entity.SeriesVisibilityPublic)),
			[]*usecase.DraftEventInput{{VenueName: lifecycleVenue, PlaceID: new(lifecyclePlaceID), LocalDate: futureDate()}},
			[]string{lifecycleArtist2})

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}

func TestSeriesUseCase_Update_Lifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	date := futureDate()
	const (
		pubID   = "019a0000-0000-7000-8000-0000000000e1"
		draftID = "019a0000-0000-7000-8000-0000000000e2"
	)

	t.Run("fail with PERMISSION_DENIED for a series of another organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "Another Organizer's series"
		d := newAuthoringDeps(t)
		other := lifecycleSeries(entity.SeriesVisibilityPublic)
		other.OrganizerID = new("019a0000-0000-7000-8000-0000000000ff")
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(other, nil, nil).Once()

		_, _, err := d.uc.Update(ctx, authoringOrgID, lifecycleSeriesID, editOf(other), nil, []string{authoringArtistID})

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("fail with PERMISSION_DENIED for an unknown series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "Unknown series"
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(nil, nil, apperr.New(codes.NotFound, "no series")).Once()

		_, _, err := d.uc.Update(ctx, authoringOrgID, lifecycleSeriesID, editOf(lifecycleSeries(entity.SeriesVisibilityPublic)), nil, nil)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("fail with INVALID_ARGUMENT when doors open after the start", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "Doors after start"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		draft := lifecycleEvent(draftID, entity.PublishStateDraft, date, nil)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{draft}, nil), nil).Once()
		representsArtists(d, authoringArtistID)
		inp := inputFor(draft)
		inp.StartTime, inp.OpenTime = at(date, 18), at(date, 19)

		_, _, err := d.uc.Update(ctx, authoringOrgID, lifecycleSeriesID, editOf(s), []*usecase.DraftEventInput{inp}, []string{authoringArtistID})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("accept a past PUBLISHED date given unchanged with a new open time", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "Correcting a past published date"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		yesterday := authoringNow.AddDate(0, 0, -1).Truncate(24 * time.Hour)
		pub := lifecycleEvent(pubID, entity.PublishStatePublished, yesterday, at(yesterday, 18))
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub}, nil), nil).Twice()
		representsArtists(d, authoringArtistID)
		d.seriesRepo.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		inp := inputFor(pub)
		inp.OpenTime = at(yesterday, 17)

		_, _, err := d.uc.Update(ctx, authoringOrgID, lifecycleSeriesID, editOf(s), []*usecase.DraftEventInput{inp}, []string{authoringArtistID})

		require.NoError(t, err)
	})

	refused := []struct {
		name  string
		setup func(d *authoringDeps) (*entity.Series, []*usecase.DraftEventInput)
	}{
		{
			// @spec components/usecase/series/update "Published per-account limit changed"
			name: "a PUBLISHED event given with another per-account limit",
			setup: func(d *authoringDeps) (*entity.Series, []*usecase.DraftEventInput) {
				s := lifecycleSeries(entity.SeriesVisibilityPublic)
				pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, nil)
				d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub}, nil), nil).Once()
				inp := inputFor(pub)
				inp.PerAccountLimit = new(2)
				return editOf(s), []*usecase.DraftEventInput{inp}
			},
		},
		{
			// @spec components/usecase/series/update "Published venue changed"
			name: "a PUBLISHED event given at another venue",
			setup: func(d *authoringDeps) (*entity.Series, []*usecase.DraftEventInput) {
				s := lifecycleSeries(entity.SeriesVisibilityPublic)
				pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, nil)
				d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub}, nil), nil).Once()
				inp := inputFor(pub)
				inp.VenueName, inp.PlaceID = "Budokan", new("ChIJ-budokan")
				return editOf(s), []*usecase.DraftEventInput{inp}
			},
		},
		{
			// A published date keeps its date as well as its venue.
			name: "a PUBLISHED event given with another date",
			setup: func(d *authoringDeps) (*entity.Series, []*usecase.DraftEventInput) {
				s := lifecycleSeries(entity.SeriesVisibilityPublic)
				pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, nil)
				d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub}, nil), nil).Once()
				inp := inputFor(pub)
				inp.LocalDate = date.AddDate(0, 0, 7)
				return editOf(s), []*usecase.DraftEventInput{inp}
			},
		},
		{
			// @spec components/usecase/series/update "Published date left out"
			name: "a PUBLISHED event left out",
			setup: func(d *authoringDeps) (*entity.Series, []*usecase.DraftEventInput) {
				s := lifecycleSeries(entity.SeriesVisibilityPublic)
				pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, nil)
				draft := lifecycleEvent(draftID, entity.PublishStateDraft, date.AddDate(0, 0, 1), nil)
				d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub, draft}, nil), nil).Once()
				return editOf(s), []*usecase.DraftEventInput{inputFor(draft)}
			},
		},
		{
			// @spec components/usecase/series/update "Visibility changed after publish"
			name: "visibility changed from PUBLIC to UNLISTED with a PUBLISHED event",
			setup: func(d *authoringDeps) (*entity.Series, []*usecase.DraftEventInput) {
				s := lifecycleSeries(entity.SeriesVisibilityPublic)
				pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, nil)
				d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub}, nil), nil).Once()
				return editOf(lifecycleSeries(entity.SeriesVisibilityUnlisted)), []*usecase.DraftEventInput{inputFor(pub)}
			},
		},
		{
			// @spec components/usecase/series/update "Start time on sale"
			name: "a PUBLISHED event's start time changed while a TicketSale offers it",
			setup: func(d *authoringDeps) (*entity.Series, []*usecase.DraftEventInput) {
				s := lifecycleSeries(entity.SeriesVisibilityPublic)
				pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, at(date, 18))
				d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub}, nil), nil).Once()
				d.ticketSales.EXPECT().ListByEvent(mock.Anything, pubID).Return([]*entity.TicketSale{{}}, nil).Once()
				inp := inputFor(pub)
				inp.StartTime = at(date, 19)
				return editOf(s), []*usecase.DraftEventInput{inp}
			},
		},
	}
	for _, tt := range refused {
		t.Run("fail with FAILED_PRECONDITION and change nothing for "+tt.name, func(t *testing.T) {
			t.Parallel()
			d := newAuthoringDeps(t)
			representsArtists(d, authoringArtistID)
			edit, inputs := tt.setup(d)

			_, _, err := d.uc.Update(ctx, authoringOrgID, lifecycleSeriesID, edit, inputs, []string{authoringArtistID})

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			d.seriesRepo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			d.venueRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
		})
	}

	// updated runs a successful Update and returns the events passed to
	// Series.Update.
	updated := func(t *testing.T, d *authoringDeps, s *entity.Series, stored []*entity.Event, inputs []*usecase.DraftEventInput) []*entity.Event {
		t.Helper()
		var given []*entity.Event
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf(stored, nil), nil).Twice()
		d.seriesRepo.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything, []string{authoringArtistID}).
			Run(func(_ context.Context, _ *entity.Series, events []*entity.Event, _ []string) { given = events }).
			Return(nil).Once()

		_, _, err := d.uc.Update(ctx, authoringOrgID, lifecycleSeriesID, editOf(s), inputs, []string{authoringArtistID})

		require.NoError(t, err)
		d.publisher.AssertNotCalled(t, "PublishEvent", mock.Anything, mock.Anything, mock.Anything)
		return given
	}

	t.Run("correct a PUBLISHED event whose Venue has no place id", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "Published date without a place id"
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)
		pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, nil)
		pub.Venue = &entity.Venue{ID: lifecycleVenueID, Name: "Zepp Haneda (TOKYO)"}
		inp := inputFor(pub)
		inp.PlaceID = nil
		edit := lifecycleSeries(entity.SeriesVisibilityPublic)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		edit.Title = "XX Tour 2026 (追加公演あり)"

		var given []*entity.Event
		var series *entity.Series
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub}, nil), nil).Twice()
		d.seriesRepo.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything, []string{authoringArtistID}).
			Run(func(_ context.Context, sr *entity.Series, events []*entity.Event, _ []string) {
				series, given = sr, events
			}).
			Return(nil).Once()

		_, _, err := d.uc.Update(ctx, authoringOrgID, lifecycleSeriesID, editOf(edit), []*usecase.DraftEventInput{inp}, []string{authoringArtistID})

		require.NoError(t, err)
		assert.Equal(t, "XX Tour 2026 (追加公演あり)", series.Title)
		require.Len(t, given, 1)
		assert.Equal(t, lifecycleVenueID, given[0].VenueID)
	})

	t.Run("refuse a new date without a place id", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "New date without a place id"
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, nil)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub}, nil), nil).Once()
		added := &usecase.DraftEventInput{VenueName: "Zepp Osaka Bayside", LocalDate: futureDate()}

		_, _, err := d.uc.Update(ctx, authoringOrgID, lifecycleSeriesID, editOf(s), []*usecase.DraftEventInput{inputFor(pub), added}, []string{authoringArtistID})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		d.seriesRepo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("set the start time of a PUBLISHED event no TicketSale offers", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "Start time before any sale"
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)
		pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, nil)
		d.ticketSales.EXPECT().ListByEvent(mock.Anything, pubID).Return(nil, nil).Once()
		inp := inputFor(pub)
		inp.StartTime = at(date, 18)

		given := updated(t, d, lifecycleSeries(entity.SeriesVisibilityPublic), []*entity.Event{pub}, []*usecase.DraftEventInput{inp})

		require.Len(t, given, 1)
		assert.Equal(t, pubID, given[0].ID)
		assert.True(t, given[0].StartTime.Equal(*at(date, 18)))
		assert.Equal(t, lifecycleVenueID, given[0].VenueID)
	})

	t.Run("pass only the DRAFT event given, so the one left out is removed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "Event removed"
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, lifecyclePlaceID).Return(&entity.Venue{ID: lifecycleVenueID}, nil).Once()
		kept := lifecycleEvent(draftID, entity.PublishStateDraft, date, nil)
		dropped := lifecycleEvent("019a0000-0000-7000-8000-0000000000e3", entity.PublishStateDraft, date.AddDate(0, 0, 1), nil)

		given := updated(t, d, lifecycleSeries(entity.SeriesVisibilityPublic), []*entity.Event{kept, dropped}, []*usecase.DraftEventInput{inputFor(kept)})

		require.Len(t, given, 1)
		assert.Equal(t, draftID, given[0].ID)
	})

	t.Run("correct the title and a PUBLISHED event's doors-open time without announcing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "Published concert corrected"
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, at(date, 18))
		inp := inputFor(pub)
		inp.OpenTime = at(date, 17)
		var edit *entity.Series
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{pub}, nil), nil).Twice()
		d.seriesRepo.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Run(func(_ context.Context, series *entity.Series, events []*entity.Event, _ []string) {
				edit = series
				require.Len(t, events, 1)
				assert.True(t, events[0].OpenTime.Equal(*at(date, 17)))
				assert.True(t, events[0].HasPublishState(entity.PublishStatePublished))
			}).Return(nil).Once()
		next := editOf(s)
		next.Title = "ARENA TOUR 2026 FINAL"

		_, _, err := d.uc.Update(ctx, authoringOrgID, lifecycleSeriesID, next, []*usecase.DraftEventInput{inp}, []string{authoringArtistID})

		require.NoError(t, err)
		assert.Equal(t, "ARENA TOUR 2026 FINAL", edit.Title)
		d.publisher.AssertNotCalled(t, "PublishEvent", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("add an event without an id to a published series as a new date", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/update "Additional date added as a draft"
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, lifecyclePlaceID).Return(&entity.Venue{ID: lifecycleVenueID}, nil).Once()
		pub := lifecycleEvent(pubID, entity.PublishStatePublished, date, nil)
		added := &usecase.DraftEventInput{VenueName: lifecycleVenue, PlaceID: new(lifecyclePlaceID), LocalDate: date.AddDate(0, 0, 7)}

		given := updated(t, d, lifecycleSeries(entity.SeriesVisibilityPublic), []*entity.Event{pub}, []*usecase.DraftEventInput{inputFor(pub), added})

		require.Len(t, given, 2)
		assert.Empty(t, given[1].ID, "a new date has no id until Series.Update stores it")
		assert.True(t, given[1].HasPublishState(entity.PublishStateDraft))
	})
}

func TestSeriesUseCase_Publish_Lifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	date := futureDate()
	const (
		draftA = "019a0000-0000-7000-8000-0000000000e1"
		draftB = "019a0000-0000-7000-8000-0000000000e2"
		pubC   = "019a0000-0000-7000-8000-0000000000e3"
	)
	artists := []*entity.Artist{{ID: authoringArtistID}}

	// published expects the second GetAuthored and the analytics report of a
	// successful publish.
	published := func(d *authoringDeps, s *entity.Series, after []*entity.Event, performers []*entity.Artist) {
		if after == nil {
			after = []*entity.Event{lifecycleEvent(draftA, entity.PublishStatePublished, date, nil)}
		}
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf(after, performers), nil).Once()
		d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectOrganizerConcertPublished, mock.Anything).Return(nil).Once()
	}

	t.Run("fail with PERMISSION_DENIED for a series of another organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "Another Organizer's series"
		d := newAuthoringDeps(t)
		other := lifecycleSeries(entity.SeriesVisibilityPublic)
		other.OrganizerID = new("019a0000-0000-7000-8000-0000000000ff")
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(other, nil, nil).Once()

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("fail with FAILED_PRECONDITION and publish nothing without a performer", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "No performer"
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(lifecycleSeries(entity.SeriesVisibilityPublic), concertsOf([]*entity.Event{lifecycleEvent(draftA, entity.PublishStateDraft, date, nil)}, nil), nil).Once()

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		d.seriesRepo.AssertNotCalled(t, "PublishEvents", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("publish a draft with no description or cover image", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "No cover image"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(s, concertsOf([]*entity.Event{lifecycleEvent(draftA, entity.PublishStateDraft, date, nil)}, artists), nil).Once()
		d.seriesRepo.EXPECT().PublishEvents(mock.Anything, lifecycleSeriesID, []string{draftA}).Return([]string{draftA}, nil).Once()
		published(d, s, nil, artists)
		d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectConcertCreated, mock.Anything).Return(nil).Once()

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		require.NoError(t, err)
	})

	t.Run("announce nothing when every published draft claimed a discovered event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "Claims a discovered event"
		// @spec components/usecase/series/publish "Only claimed events"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(s, concertsOf([]*entity.Event{lifecycleEvent(draftA, entity.PublishStateDraft, date, nil)}, artists), nil).Once()
		d.seriesRepo.EXPECT().PublishEvents(mock.Anything, lifecycleSeriesID, []string{draftA}).Return(nil, nil).Once()
		published(d, s, nil, artists)

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		require.NoError(t, err)
		d.publisher.AssertNotCalled(t, "PublishEvent", mock.Anything, entity.SubjectConcertCreated, mock.Anything)
	})

	for _, visibility := range []entity.SeriesVisibility{entity.SeriesVisibilityPublic, entity.SeriesVisibilityUnlisted} {
		t.Run("fail with FAILED_PRECONDITION and announce nothing when another series of the organizer holds the slot of a "+string(visibility)+" draft", func(t *testing.T) {
			t.Parallel()
			// @spec components/usecase/series/publish "Own other concert holds the slot"
			d := newAuthoringDeps(t)
			d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
				Return(lifecycleSeries(visibility), concertsOf([]*entity.Event{lifecycleEvent(draftA, entity.PublishStateDraft, date, nil)}, artists), nil).Once()
			d.seriesRepo.EXPECT().PublishEvents(mock.Anything, lifecycleSeriesID, []string{draftA}).
				Return(nil, apperr.New(codes.FailedPrecondition, "the event's slot is held by another series' event")).Once()

			_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			d.publisher.AssertNotCalled(t, "PublishEvent", mock.Anything, mock.Anything, mock.Anything)
			d.seriesRepo.AssertNotCalled(t, "SetUnlistedToken", mock.Anything, mock.Anything, mock.Anything)
		})
	}

	t.Run("publish a draft at a slot an admin's delete suppressed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "Suppressed slot"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(s, concertsOf([]*entity.Event{lifecycleEvent(draftA, entity.PublishStateDraft, date, nil)}, artists), nil).Once()
		// Series.PublishEvents no longer refuses a suppressed slot.
		d.seriesRepo.EXPECT().PublishEvents(mock.Anything, lifecycleSeriesID, []string{draftA}).Return([]string{draftA}, nil).Once()
		published(d, s, nil, artists)
		d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectConcertCreated, mock.Anything).Return(nil).Once()

		_, concerts, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		require.NoError(t, err)
		require.Len(t, concerts, 1)
		assert.True(t, concerts[0].HasPublishState(entity.PublishStatePublished))
	})

	t.Run("fail with FAILED_PRECONDITION when the series has no DRAFT event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "Already published"
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(lifecycleSeries(entity.SeriesVisibilityPublic), concertsOf([]*entity.Event{lifecycleEvent(pubC, entity.PublishStatePublished, date, nil)}, artists), nil).Once()

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("publish the one DRAFT date of a published tour and announce only it", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "Additional date on a published tour"
		// @spec components/usecase/series/publish "Additional date announced alone"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{
			lifecycleEvent(pubC, entity.PublishStatePublished, date, nil),
			lifecycleEvent(draftA, entity.PublishStateDraft, date.AddDate(0, 0, 1), nil),
		}, artists), nil).Once()
		d.seriesRepo.EXPECT().PublishEvents(mock.Anything, lifecycleSeriesID, []string{draftA}).Return([]string{draftA}, nil).Once()
		published(d, s, nil, artists)
		d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectConcertCreated, entity.ConcertCreatedData{
			ArtistID: authoringArtistID, ConcertIDs: []string{draftA},
		}).Return(nil).Once()

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		require.NoError(t, err)
	})

	t.Run("publish only the named date of two drafts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "Only the named date"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{
			lifecycleEvent(draftA, entity.PublishStateDraft, date, nil),
			lifecycleEvent(draftB, entity.PublishStateDraft, date.AddDate(0, 0, 1), nil),
		}, artists), nil).Once()
		d.seriesRepo.EXPECT().PublishEvents(mock.Anything, lifecycleSeriesID, []string{draftB}).Return([]string{draftB}, nil).Once()
		published(d, s, nil, artists)
		d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectConcertCreated, mock.Anything).Return(nil).Once()

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, []string{draftB})

		require.NoError(t, err)
	})

	t.Run("give an UNLISTED draft a share token, announce nothing and still report the publish", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "Unlisted publish"
		// @spec components/usecase/series/publish "Unlisted publish reported"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityUnlisted)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(s, concertsOf([]*entity.Event{lifecycleEvent(draftA, entity.PublishStateDraft, date, nil)}, artists), nil).Once()
		d.seriesRepo.EXPECT().PublishEvents(mock.Anything, lifecycleSeriesID, []string{draftA}).Return([]string{draftA}, nil).Once()
		d.seriesRepo.EXPECT().SetUnlistedToken(mock.Anything, lifecycleSeriesID, mock.Anything).Return(nil).Once()
		published(d, s, nil, artists)

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		require.NoError(t, err)
		d.publisher.AssertNotCalled(t, "PublishEvent", mock.Anything, entity.SubjectConcertCreated, mock.Anything)
	})

	t.Run("keep the share token when a new date of an UNLISTED series is published", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "New date keeps the share link"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityUnlisted)
		s.UnlistedToken = new("existing-token")
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(s, concertsOf([]*entity.Event{
			lifecycleEvent(pubC, entity.PublishStatePublished, date, nil),
			lifecycleEvent(draftA, entity.PublishStateDraft, date.AddDate(0, 0, 1), nil),
		}, artists), nil).Once()
		d.seriesRepo.EXPECT().PublishEvents(mock.Anything, lifecycleSeriesID, []string{draftA}).Return([]string{draftA}, nil).Once()
		published(d, s, nil, artists)

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		require.NoError(t, err)
		d.seriesRepo.AssertNotCalled(t, "SetUnlistedToken", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("announce the added event once per performer of a PUBLIC series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/publish "Two performers"
		d := newAuthoringDeps(t)
		s := lifecycleSeries(entity.SeriesVisibilityPublic)
		two := []*entity.Artist{{ID: authoringArtistID}, {ID: lifecycleArtist2}}
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(s, concertsOf([]*entity.Event{lifecycleEvent(draftA, entity.PublishStateDraft, date, nil)}, two), nil).Once()
		d.seriesRepo.EXPECT().PublishEvents(mock.Anything, lifecycleSeriesID, []string{draftA}).Return([]string{draftA}, nil).Once()
		published(d, s, nil, two)
		for _, a := range two {
			d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectConcertCreated, entity.ConcertCreatedData{
				ArtistID: a.ID, ConcertIDs: []string{draftA},
			}).Return(nil).Once()
		}

		_, _, err := d.uc.Publish(ctx, authoringOrgID, lifecycleSeriesID, nil)

		require.NoError(t, err)
	})
}

func TestSeriesUseCase_Cancel_Lifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	date := futureDate()
	ids := []string{
		"019a0000-0000-7000-8000-0000000000e1",
		"019a0000-0000-7000-8000-0000000000e2",
		"019a0000-0000-7000-8000-0000000000e3",
	}
	eventsIn := func(state entity.PublishState, n int) []*entity.Event {
		events := make([]*entity.Event, n)
		for i := range n {
			events[i] = lifecycleEvent(ids[i], state, date.AddDate(0, 0, i), nil)
		}
		return events
	}

	t.Run("fail with PERMISSION_DENIED for an unknown series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/cancel "Unknown series"
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).Return(nil, nil, apperr.New(codes.NotFound, "no series")).Once()

		err := d.uc.Cancel(ctx, authoringOrgID, lifecycleSeriesID, nil)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("cancel every PUBLISHED event of a tour and announce them together", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/cancel "Cancel a published concert"
		// @spec components/usecase/series/cancel "Cancelled tour"
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(lifecycleSeries(entity.SeriesVisibilityPublic), concertsOf(eventsIn(entity.PublishStatePublished, 3), nil), nil).Once()
		d.seriesRepo.EXPECT().CancelEvents(mock.Anything, lifecycleSeriesID, ids).Return(ids, nil).Once()
		d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectConcertCancelled, entity.ConcertCancelledData{
			SeriesID: lifecycleSeriesID, ConcertIDs: ids,
		}).Return(nil).Once()

		require.NoError(t, d.uc.Cancel(ctx, authoringOrgID, lifecycleSeriesID, nil))
	})

	t.Run("discard an all-DRAFT series and announce nothing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/cancel "Cancel a draft"
		// @spec components/usecase/series/cancel "Draft discarded silently"
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(lifecycleSeries(entity.SeriesVisibilityPublic), concertsOf(eventsIn(entity.PublishStateDraft, 2), nil), nil).Once()
		d.seriesRepo.EXPECT().CancelEvents(mock.Anything, lifecycleSeriesID, ids[:2]).Return(nil, nil).Once()

		require.NoError(t, d.uc.Cancel(ctx, authoringOrgID, lifecycleSeriesID, nil))
		d.publisher.AssertNotCalled(t, "PublishEvent", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("fail with FAILED_PRECONDITION when every event is already CANCELLED", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/cancel "Cancel twice"
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(lifecycleSeries(entity.SeriesVisibilityPublic), concertsOf(eventsIn(entity.PublishStateCancelled, 2), nil), nil).Once()

		err := d.uc.Cancel(ctx, authoringOrgID, lifecycleSeriesID, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		d.seriesRepo.AssertNotCalled(t, "CancelEvents", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("cancel only the named date of three", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/cancel "Cancel one date"
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, lifecycleSeriesID).
			Return(lifecycleSeries(entity.SeriesVisibilityPublic), concertsOf(eventsIn(entity.PublishStatePublished, 3), nil), nil).Once()
		d.seriesRepo.EXPECT().CancelEvents(mock.Anything, lifecycleSeriesID, []string{ids[1]}).Return([]string{ids[1]}, nil).Once()
		d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectConcertCancelled, entity.ConcertCancelledData{
			SeriesID: lifecycleSeriesID, ConcertIDs: []string{ids[1]},
		}).Return(nil).Once()

		require.NoError(t, d.uc.Cancel(ctx, authoringOrgID, lifecycleSeriesID, []string{ids[1]}))
	})
}

func TestSeriesUseCase_Create_PerAccountLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// created stores a draft with one event given the limit and returns the
	// stored events.
	created := func(t *testing.T, limit *int) ([]*entity.Event, error) {
		t.Helper()
		d := newAuthoringDeps(t)
		representsArtists(d, authoringArtistID)
		var stored []*entity.Event
		if limit == nil || (*limit >= 1 && *limit <= 10) {
			d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, lifecyclePlaceID).Return(&entity.Venue{ID: lifecycleVenueID}, nil).Once()
			d.seriesRepo.EXPECT().CreateDraft(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Run(func(_ context.Context, _ *entity.Series, events []*entity.Event, _ []string) { stored = events }).
				Return(nil).Once()
			d.seriesRepo.EXPECT().GetAuthored(mock.Anything, mock.Anything).Return(lifecycleSeries(entity.SeriesVisibilityPublic), nil, nil).Once()
		}
		_, _, err := d.uc.Create(ctx, authoringOrgID, editOf(lifecycleSeries(entity.SeriesVisibilityPublic)),
			[]*usecase.DraftEventInput{{VenueName: lifecycleVenue, PlaceID: new(lifecyclePlaceID), LocalDate: futureDate(), PerAccountLimit: limit}},
			[]string{authoringArtistID})
		if limit != nil && (*limit < 1 || *limit > 10) {
			d.seriesRepo.AssertNotCalled(t, "CreateDraft", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			d.venueRepo.AssertNotCalled(t, "GetByPlaceID", mock.Anything, mock.Anything)
		}
		return stored, err
	}

	t.Run("no limit given", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/create "No limit given"
		stored, err := created(t, nil)

		require.NoError(t, err)
		require.Len(t, stored, 1)
		assert.Equal(t, 4, stored[0].Limit())
	})

	t.Run("limit of 2", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/create "Limit of 2"
		stored, err := created(t, new(2))

		require.NoError(t, err)
		require.Len(t, stored, 1)
		assert.Equal(t, 2, stored[0].Limit())
	})

	t.Run("limit above one admission code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/create "Limit above one admission code"
		_, err := created(t, new(11))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
