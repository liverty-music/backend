package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

var (
	testVenueA  = "019a0000-0000-7000-8000-00000000000a"
	testVenueB  = "019a0000-0000-7000-8000-00000000000b"
	testOrgID   = "019a0000-0000-7000-8000-000000000001"
	testDate    = time.Date(2026, 12, 24, 0, 0, 0, 0, time.UTC)
	testAt13    = time.Date(2026, 12, 24, 4, 0, 0, 0, time.UTC)
	testAt18    = time.Date(2026, 12, 24, 9, 0, 0, 0, time.UTC)
	testAt17_30 = time.Date(2026, 12, 24, 8, 30, 0, 0, time.UTC)
)

//go:fix inline

//go:fix inline

func eventAt(venueID string, start *time.Time, state *entity.PublishState) *entity.Event {
	return &entity.Event{
		ID:           entity.NewID(),
		VenueID:      venueID,
		LocalDate:    testDate,
		StartTime:    start,
		PublishState: state,
	}
}

func firstPartySeries(visibility entity.SeriesVisibility) *entity.Series {
	org := testOrgID
	return &entity.Series{OrganizerID: &org, Visibility: &visibility}
}

func TestEvent_SameSlot(t *testing.T) {
	t.Parallel()

	published := new(entity.PublishStatePublished)

	tests := []struct {
		name string
		a, b *entity.Event
		want bool
	}{
		{
			// @spec components/entity/event "Matinee and evening shows are distinct"
			name: "return false for two start times on one venue and date",
			a:    eventAt(testVenueA, new(testAt13), nil),
			b:    eventAt(testVenueA, new(testAt18), nil),
			want: false,
		},
		{
			// @spec components/entity/event "Same slot under different series is one event"
			name: "return true for one venue, date and start time under different series",
			a:    &entity.Event{SeriesID: "s1", VenueID: testVenueA, LocalDate: testDate, StartTime: new(testAt18)},
			b:    &entity.Event{SeriesID: "s2", VenueID: testVenueA, LocalDate: testDate, StartTime: new(testAt18), PublishState: published},
			want: true,
		},
		{
			// @spec components/entity/event "Two unknown start times collapse"
			name: "return true when neither has a start time",
			a:    eventAt(testVenueA, nil, nil),
			b:    eventAt(testVenueA, nil, published),
			want: true,
		},
		{
			// @spec components/entity/event "Unknown start does not match a known start"
			name: "return false for a known and an unknown start time",
			a:    eventAt(testVenueA, new(testAt18), nil),
			b:    eventAt(testVenueA, nil, nil),
			want: false,
		},
		{
			// @spec components/entity/event "Draft beside a discovered event"
			name: "return false for a draft at the slot of a discovered event",
			a:    eventAt(testVenueA, new(testAt18), new(entity.PublishStateDraft)),
			b:    eventAt(testVenueA, new(testAt18), nil),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.a.SameSlot(tt.b))
		})
	}
}

func TestEvent_PublishState(t *testing.T) {
	t.Parallel()

	t.Run("leave the publish state nil for an event of a discovered series", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Discovered event has no publish state"
		discovered := eventAt(testVenueA, nil, nil)
		assert.Nil(t, discovered.PublishState)
		assert.False(t, discovered.CanTransitionTo(entity.PublishStatePublished))
		assert.False(t, discovered.CanTransitionTo(entity.PublishStateCancelled))
	})

	t.Run("start a new authored event as DRAFT", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "New authored event is a draft"
		ev := entity.NewAuthoredEvent("series", testVenueA, nil, testDate, nil, nil)
		assert.True(t, ev.HasPublishState(entity.PublishStateDraft))
		assert.NotEmpty(t, ev.ID)
	})

	t.Run("refuse to cancel a DRAFT event", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Draft is not cancellable"
		ev := eventAt(testVenueA, nil, new(entity.PublishStateDraft))
		assert.False(t, ev.CanTransitionTo(entity.PublishStateCancelled))
		assert.True(t, ev.CanTransitionTo(entity.PublishStatePublished))
	})

	t.Run("refuse every transition from CANCELLED", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Cancelled stays cancelled"
		ev := eventAt(testVenueA, nil, new(entity.PublishStateCancelled))
		assert.False(t, ev.CanTransitionTo(entity.PublishStateDraft))
		assert.False(t, ev.CanTransitionTo(entity.PublishStatePublished))
	})
}

func TestEvent_IsPubliclyVisible(t *testing.T) {
	t.Parallel()

	public := firstPartySeries(entity.SeriesVisibilityPublic)
	unlisted := firstPartySeries(entity.SeriesVisibilityUnlisted)

	tests := []struct {
		name   string
		event  *entity.Event
		series *entity.Series
		want   bool
	}{
		{
			// @spec components/entity/event "Discovered event is visible"
			name:   "return true for an event of a discovered series",
			event:  eventAt(testVenueA, nil, nil),
			series: &entity.Series{},
			want:   true,
		},
		{
			// @spec components/entity/event "Published event of a public series is visible"
			name:   "return true for a PUBLISHED event of a PUBLIC series",
			event:  eventAt(testVenueA, nil, new(entity.PublishStatePublished)),
			series: public,
			want:   true,
		},
		{
			// @spec components/entity/event "Published event of an unlisted series is not visible"
			name:   "return false for a PUBLISHED event of an UNLISTED series",
			event:  eventAt(testVenueA, nil, new(entity.PublishStatePublished)),
			series: unlisted,
			want:   false,
		},
		{
			// @spec components/entity/event "Cancelled date is not visible"
			name:   "return false for a CANCELLED event of a PUBLIC series",
			event:  eventAt(testVenueA, nil, new(entity.PublishStateCancelled)),
			series: public,
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.event.IsPubliclyVisible(tt.series))
		})
	}

	t.Run("show only the PUBLISHED date of a PUBLIC tour with a DRAFT date", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Draft date of a published tour is not visible"
		published := eventAt(testVenueA, nil, new(entity.PublishStatePublished))
		draft := eventAt(testVenueB, nil, new(entity.PublishStateDraft))
		assert.True(t, published.IsPubliclyVisible(public))
		assert.False(t, draft.IsPubliclyVisible(public))
	})
}

func TestEvent_CheckChange(t *testing.T) {
	t.Parallel()

	published := func() *entity.Event {
		ev := eventAt(testVenueA, new(testAt18), new(entity.PublishStatePublished))
		ev.OpenTime = new(testAt17_30)
		return ev
	}
	withChange := func(ev *entity.Event, change func(*entity.Event)) *entity.Event {
		next := *ev
		change(&next)
		return &next
	}

	t.Run("reject a new venue or date for a PUBLISHED event", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Published venue is fixed"
		ev := published()
		err := ev.CheckChange(withChange(ev, func(n *entity.Event) { n.VenueID = testVenueB }), false)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		err = ev.CheckChange(withChange(ev, func(n *entity.Event) { n.LocalDate = testDate.AddDate(0, 0, 1) }), false)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("accept a new open time for a PUBLISHED event on sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Doors-open time after publish"
		ev := published()
		next := withChange(ev, func(n *entity.Event) { n.OpenTime = new(testAt17_30.Add(-30 * time.Minute)) })
		assert.NoError(t, ev.CheckChange(next, true))
	})

	t.Run("accept a first start time for a PUBLISHED event not on sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Start time announced after publish"
		ev := eventAt(testVenueA, nil, new(entity.PublishStatePublished))
		next := withChange(ev, func(n *entity.Event) { n.StartTime = new(testAt18) })
		assert.NoError(t, ev.CheckChange(next, false))
	})

	t.Run("reject a new start time for a PUBLISHED event on sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Start time on sale is fixed"
		ev := published()
		next := withChange(ev, func(n *entity.Event) { n.StartTime = new(testAt13) })
		assert.ErrorIs(t, ev.CheckChange(next, true), apperr.ErrFailedPrecondition)
	})

	t.Run("reject any change to a CANCELLED event", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Cancelled event is fixed"
		ev := published()
		ev.PublishState = new(entity.PublishStateCancelled)
		next := withChange(ev, func(n *entity.Event) { n.OpenTime = nil })
		assert.ErrorIs(t, ev.CheckChange(next, false), apperr.ErrFailedPrecondition)
		assert.NoError(t, ev.CheckChange(withChange(ev, func(*entity.Event) {}), false))
	})

	t.Run("accept any change to a DRAFT event", func(t *testing.T) {
		t.Parallel()
		ev := eventAt(testVenueA, new(testAt18), new(entity.PublishStateDraft))
		next := withChange(ev, func(n *entity.Event) { n.VenueID = testVenueB; n.StartTime = nil })
		assert.NoError(t, ev.CheckChange(next, true))
	})
}
