package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

func eventsIn(states ...entity.PublishState) []*entity.Event {
	events := make([]*entity.Event, 0, len(states))
	for _, s := range states {
		events = append(events, eventAt(testVenueA, nil, new(s)))
	}
	return events
}

func TestDerivePublishState(t *testing.T) {
	t.Parallel()

	const (
		draft     = entity.PublishStateDraft
		published = entity.PublishStatePublished
		cancelled = entity.PublishStateCancelled
	)

	tests := []struct {
		name       string
		firstParty bool
		events     []*entity.Event
		want       *entity.PublishState
	}{
		{
			// @spec components/entity/series "Published tour with a draft date"
			name:       "return PUBLISHED for two PUBLISHED events and one DRAFT",
			firstParty: true,
			events:     eventsIn(published, published, draft),
			want:       new(published),
		},
		{
			// @spec components/entity/series "One date cancelled"
			name:       "return PUBLISHED for one PUBLISHED and one CANCELLED event",
			firstParty: true,
			events:     eventsIn(published, cancelled),
			want:       new(published),
		},
		{
			// @spec components/entity/series "Every published date cancelled"
			name:       "return CANCELLED for two CANCELLED events and one DRAFT",
			firstParty: true,
			events:     eventsIn(cancelled, draft, cancelled),
			want:       new(cancelled),
		},
		{
			// @spec components/entity/series "Only drafts"
			name:       "return DRAFT when every event is DRAFT",
			firstParty: true,
			events:     eventsIn(draft, draft),
			want:       new(draft),
		},
		{
			// @spec components/entity/series "Discovered series has no publish state"
			name:       "return nil for a discovered series",
			firstParty: false,
			events:     []*entity.Event{eventAt(testVenueA, nil, nil)},
			want:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, entity.DerivePublishState(tt.firstParty, tt.events))
		})
	}
}

func TestSeries_CheckChange(t *testing.T) {
	t.Parallel()

	stored := func() *entity.Series {
		s := firstPartySeries(entity.SeriesVisibilityPublic)
		s.Title = "ARENA TOUR 2026"
		s.Type = entity.SeriesTypeTour
		s.SourceURL = "https://example.com/tour"
		return s
	}
	edited := func(change func(*entity.Series)) *entity.Series {
		s := stored()
		change(s)
		return s
	}
	withPublished := eventsIn(entity.PublishStatePublished, entity.PublishStateDraft)

	t.Run("accept a new title and description after publish", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/series "Title corrected after publish"
		next := edited(func(s *entity.Series) {
			s.Title = "ARENA TOUR 2026 FINAL"
			d := "Guest act added."
			s.Description = &d
		})
		assert.NoError(t, stored().CheckChange(next, withPublished))
	})

	t.Run("reject a new type, source page or visibility after publish", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/series "Visibility fixed after publish"
		for _, next := range []*entity.Series{
			edited(func(s *entity.Series) { s.Type = entity.SeriesTypeSingle }),
			edited(func(s *entity.Series) { s.SourceURL = "https://example.com/other" }),
			edited(func(s *entity.Series) { v := entity.SeriesVisibilityUnlisted; s.Visibility = &v }),
		} {
			assert.ErrorIs(t, stored().CheckChange(next, withPublished), apperr.ErrFailedPrecondition)
		}
	})

	t.Run("accept any change while every event is DRAFT", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/series "Draft series is fully editable"
		next := edited(func(s *entity.Series) {
			s.Type = entity.SeriesTypeSingle
			v := entity.SeriesVisibilityUnlisted
			s.Visibility = &v
		})
		assert.NoError(t, stored().CheckChange(next, eventsIn(entity.PublishStateDraft, entity.PublishStateDraft)))
	})
}

func TestEvent_HasEventPage(t *testing.T) {
	t.Parallel()

	public := firstPartySeries(entity.SeriesVisibilityPublic)

	tests := []struct {
		name   string
		event  *entity.Event
		series *entity.Series
		want   bool
	}{
		{
			// @spec components/entity/series "Published public series"
			name:   "return true for a PUBLISHED event of a PUBLIC first-party series",
			event:  eventAt(testVenueA, nil, new(entity.PublishStatePublished)),
			series: public,
			want:   true,
		},
		{
			// @spec components/entity/series "Cancelled public series"
			name:   "return true for a CANCELLED event of a PUBLIC first-party series",
			event:  eventAt(testVenueA, nil, new(entity.PublishStateCancelled)),
			series: public,
			want:   true,
		},
		{
			// @spec components/entity/series "Unlisted series"
			name:   "return false for a PUBLISHED event of an UNLISTED first-party series",
			event:  eventAt(testVenueA, nil, new(entity.PublishStatePublished)),
			series: firstPartySeries(entity.SeriesVisibilityUnlisted),
			want:   false,
		},
		{
			// @spec components/entity/series "Discovered series"
			name:   "return false for an event of a series with no organizer",
			event:  eventAt(testVenueA, nil, nil),
			series: &entity.Series{Title: "Discovered Tour"},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.event.HasEventPage(tt.series))
		})
	}

	t.Run("return false for a DRAFT event while another event is PUBLISHED", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/series "Draft series"
		published := eventAt(testVenueA, nil, new(entity.PublishStatePublished))
		draft := eventAt(testVenueB, nil, new(entity.PublishStateDraft))
		assert.False(t, draft.HasEventPage(public))
		assert.True(t, public.HasAnyEventPage([]*entity.Event{published, draft}))
		assert.False(t, public.HasAnyEventPage([]*entity.Event{draft}))
	})
}
