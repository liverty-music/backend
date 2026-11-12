package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

func storedEvent(id string, state entity.PublishState, start *time.Time) *entity.Event {
	return &entity.Event{ID: id, PublishState: &state, StartTime: start}
}

func TestEvent_SlotMergePlan(t *testing.T) {
	t.Parallel()

	e := &entity.Event{ID: "e-1"}
	organizer := "org-1"

	merge, err := e.SlotMergePlan(nil)
	require.NoError(t, err)
	assert.Nil(t, merge, "a free slot merges nothing")

	holder := &entity.SlotHolder{EventID: "disc-1", SeriesID: "s-disc"}
	merge, err = e.SlotMergePlan(holder)
	require.NoError(t, err)
	assert.Same(t, holder, merge, "a discovered event at the slot is merged into the event")

	_, err = e.SlotMergePlan(&entity.SlotHolder{EventID: "e-2", SeriesID: "s-2", OrganizerID: &organizer})
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "an event of another first-party series is never merged")
}

func TestEvent_ClaimSlotPlan(t *testing.T) {
	t.Parallel()

	draft := &entity.Event{ID: "d-1"}
	organizer := "org-1"
	cancelled, published := entity.PublishStateCancelled, entity.PublishStatePublished
	tests := []struct {
		name    string
		holder  *entity.SlotHolder
		want    entity.SlotClaim
		wantErr bool
	}{
		{"free slot", nil, entity.SlotClaimPublish, false},
		{"held by a cancelled event", &entity.SlotHolder{EventID: "h", SeriesID: "s-1", State: &cancelled, OrganizerID: &organizer}, 0, true},
		{"held by a cancelled event of another series", &entity.SlotHolder{EventID: "h", SeriesID: "s-2", State: &cancelled}, 0, true},
		{"held by a published event of the same series", &entity.SlotHolder{EventID: "h", SeriesID: "s-1", State: &published, OrganizerID: &organizer}, entity.SlotClaimExisting, false},
		{"held by another first-party series", &entity.SlotHolder{EventID: "h", SeriesID: "s-2", State: &published, OrganizerID: &organizer}, 0, true},
		{"held by a discovered event", &entity.SlotHolder{EventID: "h", SeriesID: "s-disc"}, entity.SlotClaimDiscovered, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := draft.ClaimSlotPlan("s-1", tt.holder)
			if tt.wantErr {
				assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPlanSeriesUpdate(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 1, 18, 0, 0, 0, time.UTC)
	later := start.Add(time.Hour)
	stored := map[string]*entity.Event{
		"draft-1":     storedEvent("draft-1", entity.PublishStateDraft, nil),
		"draft-2":     storedEvent("draft-2", entity.PublishStateDraft, nil),
		"published-1": storedEvent("published-1", entity.PublishStatePublished, &start),
		"published-2": storedEvent("published-2", entity.PublishStatePublished, &start),
		"cancelled-1": storedEvent("cancelled-1", entity.PublishStateCancelled, &start),
	}

	t.Run("each given event by its state, drafts left out are removed", func(t *testing.T) {
		t.Parallel()
		given := []*entity.Event{
			{ID: "draft-1"},
			{},
			{ID: "published-1", StartTime: &later},
			{ID: "published-2", StartTime: &start},
			{ID: "cancelled-1"},
		}

		plan, err := entity.PlanSeriesUpdate(stored, given)

		require.NoError(t, err)
		kinds := make([]entity.EventEditKind, len(plan.Edits))
		for i, e := range plan.Edits {
			kinds[i] = e.Kind
		}
		assert.Equal(t, []entity.EventEditKind{
			entity.EventEditDraft, entity.EventEditAdd, entity.EventEditPublishedTimes, entity.EventEditPublishedTimes, entity.EventEditNone,
		}, kinds, "in the order the events were given")
		assert.True(t, plan.Edits[2].MovesSlot, "a changed start time takes the slot of the new one")
		assert.False(t, plan.Edits[3].MovesSlot, "an unchanged start time does not")
		assert.Equal(t, []string{"draft-2"}, plan.Removed, "only a DRAFT event left out is removed")
	})

	t.Run("a start time set on a published event with none moves its slot", func(t *testing.T) {
		t.Parallel()
		stored := map[string]*entity.Event{"p": storedEvent("p", entity.PublishStatePublished, nil)}
		plan, err := entity.PlanSeriesUpdate(stored, []*entity.Event{{ID: "p", StartTime: &start}})
		require.NoError(t, err)
		assert.True(t, plan.Edits[0].MovesSlot)
	})

	t.Run("an event that is not the series'", func(t *testing.T) {
		t.Parallel()
		_, err := entity.PlanSeriesUpdate(stored, []*entity.Event{{ID: "elsewhere"}})
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

func TestCheckPublishable(t *testing.T) {
	t.Parallel()

	stored := map[string]*entity.Event{
		"draft":     storedEvent("draft", entity.PublishStateDraft, nil),
		"published": storedEvent("published", entity.PublishStatePublished, nil),
	}
	assert.NoError(t, entity.CheckPublishable(stored, []string{"draft"}))
	assert.ErrorIs(t, entity.CheckPublishable(stored, []string{"draft", "published"}), apperr.ErrFailedPrecondition)
	assert.ErrorIs(t, entity.CheckPublishable(stored, []string{"missing"}), apperr.ErrFailedPrecondition)
}

func TestPlanCancel(t *testing.T) {
	t.Parallel()

	stored := map[string]*entity.Event{
		"draft":     storedEvent("draft", entity.PublishStateDraft, nil),
		"published": storedEvent("published", entity.PublishStatePublished, nil),
		"cancelled": storedEvent("cancelled", entity.PublishStateCancelled, nil),
	}

	plan, err := entity.PlanCancel(stored, []string{"published", "draft"})
	require.NoError(t, err)
	assert.Equal(t, entity.CancelPlan{Cancel: []string{"published"}, Remove: []string{"draft"}}, plan, "a published event is cancelled, a draft removed")

	_, err = entity.PlanCancel(stored, []string{"cancelled"})
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "already cancelled")
	_, err = entity.PlanCancel(stored, []string{"published", "missing"})
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "not an event of the series")
}
