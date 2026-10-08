package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	eventPageSeriesID = "019a0000-0000-7000-8000-0000000000a1"
	eventPageEventID  = "019a0000-0000-7000-8000-0000000000e1"
)

// eventPageSeries builds a first-party Series with the given visibility and
// publish state, as SeriesRepository.Get returns it.
func eventPageSeries(visibility entity.SeriesVisibility, state entity.SeriesPublishState) *entity.Series {
	organizerID := "019a0000-0000-7000-8000-0000000000f1"
	description := "Two nights at Shibuya WWW."
	return &entity.Series{
		ID:           eventPageSeriesID,
		Title:        "ONE MAN LIVE",
		Type:         entity.SeriesTypeSingle,
		Description:  &description,
		CoverMedia:   &entity.Media{ID: "019a0000-0000-7000-8000-0000000000c1", Kind: entity.MediaKindImage},
		OrganizerID:  &organizerID,
		Visibility:   &visibility,
		PublishState: &state,
	}
}

// eventPageConcert builds a Concert as ConcertRepository.ListByIDs returns it:
// its Series carries only the list columns (no first-party attributes).
func eventPageConcert(id string, date time.Time, start *time.Time) *entity.Concert {
	return &entity.Concert{
		ID:         id,
		SeriesID:   eventPageSeriesID,
		LocalDate:  date,
		StartTime:  start,
		Series:     &entity.Series{ID: eventPageSeriesID, Title: "ONE MAN LIVE", Type: entity.SeriesTypeSingle},
		Performers: []*entity.Artist{{ID: "019a0000-0000-7000-8000-0000000000b1", Name: "The Band"}},
	}
}

func TestConcertUseCase_Get(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		setup     func(t *testing.T, d *concertTestDeps)
		wantState entity.SeriesPublishState
		wantCode  error
	}{
		{
			// @spec components/usecase/concert/get "Published public concert"
			name: "return the concert with its full series when the series is PUBLISHED PUBLIC",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).
					Return([]*entity.Concert{eventPageConcert(eventPageEventID, date, nil)}, nil).Once()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
					Return(eventPageSeries(entity.SeriesVisibilityPublic, entity.SeriesPublishStatePublished), nil).Once()
			},
			wantState: entity.SeriesPublishStatePublished,
		},
		{
			// @spec components/usecase/concert/get "Cancelled series"
			name: "return the concert when the series is CANCELLED PUBLIC",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).
					Return([]*entity.Concert{eventPageConcert(eventPageEventID, date, nil)}, nil).Once()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
					Return(eventPageSeries(entity.SeriesVisibilityPublic, entity.SeriesPublishStateCancelled), nil).Once()
			},
			wantState: entity.SeriesPublishStateCancelled,
		},
		{
			// @spec components/usecase/concert/get "Unknown id"
			name: "return NotFound when no concert has the id",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).Return(nil, nil).Once()
			},
			wantCode: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/concert/get "Unlisted series"
			name: "return NotFound when the series is UNLISTED",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).
					Return([]*entity.Concert{eventPageConcert(eventPageEventID, date, nil)}, nil).Once()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
					Return(eventPageSeries(entity.SeriesVisibilityUnlisted, entity.SeriesPublishStatePublished), nil).Once()
			},
			wantCode: apperr.ErrNotFound,
		},
		{
			name: "return NotFound when the series is DRAFT",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).
					Return([]*entity.Concert{eventPageConcert(eventPageEventID, date, nil)}, nil).Once()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
					Return(eventPageSeries(entity.SeriesVisibilityPublic, entity.SeriesPublishStateDraft), nil).Once()
			},
			wantCode: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/concert/get "Discovered concert"
			name: "return NotFound when the series has no organizer",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).
					Return([]*entity.Concert{eventPageConcert(eventPageEventID, date, nil)}, nil).Once()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
					Return(&entity.Series{ID: eventPageSeriesID, Title: "Discovered Tour"}, nil).Once()
			},
			wantCode: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/concert/get "Store unavailable"
			name: "return Unavailable unchanged when Series.Get fails",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).
					Return([]*entity.Concert{eventPageConcert(eventPageEventID, date, nil)}, nil).Once()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
					Return(nil, apperr.New(codes.Unavailable, "db down")).Once()
			},
			wantCode: apperr.ErrUnavailable,
		},
		{
			name: "return Unavailable unchanged when Concert.ListByIDs fails",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).
					Return(nil, apperr.New(codes.Unavailable, "db down")).Once()
			},
			wantCode: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newConcertTestDeps(t)
			tt.setup(t, d)

			got, err := d.uc.Get(ctx, eventPageEventID)

			if tt.wantCode != nil {
				assert.True(t, errors.Is(err, tt.wantCode), "expected %v, got %v", tt.wantCode, err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got.Series)
			assert.Equal(t, eventPageEventID, got.ID)
			assert.Equal(t, tt.wantState, *got.Series.PublishState)
			assert.Equal(t, "Two nights at Shibuya WWW.", *got.Series.Description)
			assert.NotNil(t, got.Series.CoverMedia)
		})
	}
}

func TestConcertUseCase_Get_NotFoundIsIndistinguishable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	unknown := newConcertTestDeps(t)
	unknown.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).Return(nil, nil).Once()
	_, unknownErr := unknown.uc.Get(ctx, eventPageEventID)

	unlisted := newConcertTestDeps(t)
	unlisted.concertRepo.EXPECT().ListByIDs(ctx, []string{eventPageEventID}).
		Return([]*entity.Concert{eventPageConcert(eventPageEventID, date, nil)}, nil).Once()
	unlisted.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
		Return(eventPageSeries(entity.SeriesVisibilityUnlisted, entity.SeriesPublishStatePublished), nil).Once()
	_, unlistedErr := unlisted.uc.Get(ctx, eventPageEventID)

	require.Error(t, unknownErr)
	require.Error(t, unlistedErr)
	assert.Equal(t, unknownErr.Error(), unlistedErr.Error())
}

func TestConcertUseCase_ListBySeries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	nov20 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	nov21 := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	at := func(hour int) *time.Time {
		t := time.Date(2026, 11, 20, hour-9, 0, 0, 0, time.UTC) // JST hour
		return &t
	}
	const (
		idA = "019a0000-0000-7000-8000-0000000000e2"
		idB = "019a0000-0000-7000-8000-0000000000e3"
	)
	published := func() *entity.Series {
		return eventPageSeries(entity.SeriesVisibilityPublic, entity.SeriesPublishStatePublished)
	}

	tests := []struct {
		name     string
		setup    func(t *testing.T, d *concertTestDeps)
		wantIDs  []string
		wantCode error
	}{
		{
			// @spec components/usecase/concert/list-by-series "Two-day run"
			name: "return the 2026-11-20 concert before the 2026-11-21 concert",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).Return(published(), nil).Once()
				d.concertRepo.EXPECT().ListEventsBySeries(ctx, eventPageSeriesID).Return([]*entity.Event{
					{ID: idA, SeriesID: eventPageSeriesID, LocalDate: nov20},
					{ID: idB, SeriesID: eventPageSeriesID, LocalDate: nov21},
				}, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{idA, idB}).Return([]*entity.Concert{
					eventPageConcert(idB, nov21, nil),
					eventPageConcert(idA, nov20, nil),
				}, nil).Once()
			},
			wantIDs: []string{idA, idB},
		},
		{
			// @spec components/usecase/concert/list-by-series "Same day, two shows"
			name: "return the 13:00 concert before the 18:00 concert on the same day",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).Return(published(), nil).Once()
				d.concertRepo.EXPECT().ListEventsBySeries(ctx, eventPageSeriesID).Return([]*entity.Event{
					{ID: idB, SeriesID: eventPageSeriesID, LocalDate: nov20, StartTime: at(13)},
					{ID: idA, SeriesID: eventPageSeriesID, LocalDate: nov20, StartTime: at(18)},
				}, nil).Once()
				// ListByIDs orders by date only, so same-day rows may come back in any order.
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{idB, idA}).Return([]*entity.Concert{
					eventPageConcert(idA, nov20, at(18)),
					eventPageConcert(idB, nov20, at(13)),
				}, nil).Once()
			},
			wantIDs: []string{idB, idA},
		},
		{
			name: "return an empty list when the series has no events",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).Return(published(), nil).Once()
				d.concertRepo.EXPECT().ListEventsBySeries(ctx, eventPageSeriesID).Return(nil, nil).Once()
			},
			wantIDs: []string{},
		},
		{
			// @spec components/usecase/concert/list-by-series "Unlisted series"
			name: "return NotFound when the series is UNLISTED",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
					Return(eventPageSeries(entity.SeriesVisibilityUnlisted, entity.SeriesPublishStatePublished), nil).Once()
			},
			wantCode: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/concert/list-by-series "Discovered series"
			name: "return NotFound when the series has no organizer",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
					Return(&entity.Series{ID: eventPageSeriesID, Title: "Discovered Tour"}, nil).Once()
			},
			wantCode: apperr.ErrNotFound,
		},
		{
			name: "return NotFound when no series has the id",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).
					Return(nil, apperr.New(codes.NotFound, "series not found")).Once()
			},
			wantCode: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/concert/list-by-series "Store unavailable"
			name: "return Unavailable unchanged when Concert.ListEventsBySeries fails",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).Return(published(), nil).Once()
				d.concertRepo.EXPECT().ListEventsBySeries(ctx, eventPageSeriesID).
					Return(nil, apperr.New(codes.Unavailable, "db down")).Once()
			},
			wantCode: apperr.ErrUnavailable,
		},
		{
			name: "return Unavailable unchanged when Concert.ListByIDs fails",
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				d.seriesRepo.EXPECT().Get(ctx, eventPageSeriesID).Return(published(), nil).Once()
				d.concertRepo.EXPECT().ListEventsBySeries(ctx, eventPageSeriesID).Return([]*entity.Event{
					{ID: idA, SeriesID: eventPageSeriesID, LocalDate: nov20},
				}, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{idA}).
					Return(nil, apperr.New(codes.Unavailable, "db down")).Once()
			},
			wantCode: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newConcertTestDeps(t)
			tt.setup(t, d)

			got, err := d.uc.ListBySeries(ctx, eventPageSeriesID)

			if tt.wantCode != nil {
				assert.True(t, errors.Is(err, tt.wantCode), "expected %v, got %v", tt.wantCode, err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			gotIDs := make([]string, 0, len(got))
			for _, c := range got {
				gotIDs = append(gotIDs, c.ID)
				require.NotNil(t, c.Series)
				assert.NotNil(t, c.Series.OrganizerID, "each concert carries the full series")
			}
			assert.Equal(t, tt.wantIDs, gotIDs)
		})
	}
}
