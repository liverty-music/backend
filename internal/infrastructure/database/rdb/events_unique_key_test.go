package rdb_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// TestEventsUniqueKey checks the events unique key against the scenarios of
// the Event spec that say which events are the same performance: it refuses a
// second event exactly when the two share the venue, the local date and the
// start time (an unknown start time matching only another unknown one), and
// neither is a DRAFT.
func TestEventsUniqueKey(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	f := newVisibilityFixture(t, db)
	date := time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)
	at13 := time.Date(2027, 1, 10, 4, 0, 0, 0, time.UTC)
	at18 := time.Date(2027, 1, 10, 9, 0, 0, 0, time.UTC)
	published := new(entity.PublishStatePublished)
	draft := new(entity.PublishStateDraft)

	tests := []struct {
		name         string
		firstStart   *time.Time
		firstState   *entity.PublishState
		secondStart  *time.Time
		secondState  *entity.PublishState
		wantSameSlot bool
	}{
		// @spec components/entity/event "Matinee and evening shows are distinct"
		{"matinee and evening shows are distinct", &at13, nil, &at18, nil, false},
		// @spec components/entity/event "Same slot under different series is one event"
		{"one slot under a discovered and an Organizer series is one event", &at18, nil, &at18, published, true},
		// @spec components/entity/event "Two unknown start times collapse"
		{"two unknown start times collapse", nil, nil, nil, published, true},
		// @spec components/entity/event "Unknown start does not match a known start"
		{"an unknown start does not match a known start", &at18, nil, nil, nil, false},
		// @spec components/entity/event "Draft beside a discovered event"
		{"a draft beside a discovered event", &at18, draft, &at18, nil, false},
		{"two drafts at one slot", &at18, draft, &at18, draft, false},
		{"a discovered event beside a draft at its slot", &at18, nil, &at18, draft, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			venue := f.venue("JP-13", nil, nil)
			first := &entity.Event{VenueID: venue, LocalDate: date, StartTime: tt.firstStart, PublishState: tt.firstState}
			second := &entity.Event{VenueID: venue, LocalDate: date, StartTime: tt.secondStart, PublishState: tt.secondState}

			if err := insertSlotEvent(f, first); err != nil {
				t.Fatalf("insert the first event: %v", err)
			}
			err := insertSlotEvent(f, second)

			assert.Equal(t, tt.wantSameSlot, rdb.IsUniqueViolation(err))
		})
	}
}

// insertSlotEvent inserts e under a series of its kind: a discovered series
// for an event with no publish state, an Organizer's otherwise.
func insertSlotEvent(f *visibilityFixture, e *entity.Event) error {
	series := f.discoveredSeries()
	var state *string
	if e.PublishState != nil {
		series = f.firstPartySeries(entity.SeriesVisibilityPublic)
		s := string(*e.PublishState)
		state = &s
	}
	_, err := f.db.Pool.Exec(f.ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date, start_time, publish_state, per_account_limit)
		 VALUES ($1, $2, $3, $4, $5, $6::event_publish_state, CASE WHEN $6::event_publish_state IS NULL THEN NULL ELSE 4 END)`,
		entity.NewID(), series, e.VenueID, e.LocalDate, e.StartTime, state)
	return err
}
