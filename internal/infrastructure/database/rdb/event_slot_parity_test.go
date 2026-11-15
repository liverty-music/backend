package rdb_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// TestEventSlotParity inserts every pair of events at one venue and date,
// over unknown and known start times and the publish states a row can have,
// and checks that the events unique key refuses the second row exactly when
// entity.Event.SameSlot calls the two events the same performance.
func TestEventSlotParity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	f := newVisibilityFixture(t, db)
	date := time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)
	at13 := time.Date(2027, 1, 10, 4, 0, 0, 0, time.UTC)
	at18 := time.Date(2027, 1, 10, 9, 0, 0, 0, time.UTC)
	starts := map[string]*time.Time{"unknown": nil, "13:00": &at13, "18:00": &at18}
	states := map[string]*entity.PublishState{
		"discovered": nil,
		"draft":      new(entity.PublishStateDraft),
		"published":  new(entity.PublishStatePublished),
	}

	for aStart, as := range starts {
		for bStart, bs := range starts {
			for aState, ap := range states {
				for bState, bp := range states {
					venue := f.venue("JP-13", nil, nil)
					a := &entity.Event{VenueID: venue, LocalDate: date, StartTime: as, PublishState: ap}
					b := &entity.Event{VenueID: venue, LocalDate: date, StartTime: bs, PublishState: bp}
					name := fmt.Sprintf("%s %s then %s %s", aState, aStart, bState, bStart)

					insertSlotEvent(t, f, a)
					err := tryInsertSlotEvent(f, b)

					assert.Equal(t, a.SameSlot(b), rdb.IsUniqueViolation(err), name)
				}
			}
		}
	}
}

// insertSlotEvent inserts e under a series of its kind: a discovered series
// for an event with no publish state, an Organizer's otherwise.
func insertSlotEvent(t *testing.T, f *visibilityFixture, e *entity.Event) {
	t.Helper()
	if err := tryInsertSlotEvent(f, e); err != nil {
		t.Fatalf("insert the first event: %v", err)
	}
}

func tryInsertSlotEvent(f *visibilityFixture, e *entity.Event) error {
	series := f.discoveredSeries()
	var state *string
	if e.PublishState != nil {
		series = f.firstPartySeries(entity.SeriesVisibilityPublic)
		s := string(*e.PublishState)
		state = &s
	}
	_, err := f.db.Pool.Exec(f.ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date, start_at, publish_state, per_account_limit)
		 VALUES ($1, $2, $3, $4, $5, $6::event_publish_state, CASE WHEN $6::event_publish_state IS NULL THEN NULL ELSE 4 END)`,
		entity.NewID(), series, e.VenueID, e.LocalDate, e.StartTime, state)
	return err
}
