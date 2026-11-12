package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"uuid"
)

// TestEventConditionsParity checks that the SQL fragments of event_conditions.go
// select exactly what the entity predicates accept, for every combination of
// the inputs they read: a discovered or a first-party series, PUBLIC or
// UNLISTED, and the event's publish state (DRAFT, PUBLISHED or CANCELLED; a
// discovered event has none).
//
//   - fanVisibleCondition against Event.IsPubliclyVisible,
//   - catalogEventCondition against Event.IsInCatalog,
//   - seriesPublishStateColumn against DerivePublishState.
func TestEventConditionsParity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	organizerID := seedOrganizer(t, db)
	venueID := seedVenue(t, db, "parity-venue-"+uuid.NewV7().String())
	day := 0
	insertEvent := func(seriesID string, state *entity.PublishState) string {
		day++
		id := uuid.NewV7().String()
		var publishState, limit any
		if state != nil {
			publishState, limit = string(*state), 4
		}
		_, err := db.Pool.Exec(ctx, `INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state, per_account_limit)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			id, seriesID, venueID, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, day), publishState, limit)
		require.NoError(t, err)
		return id
	}

	type seriesCase struct {
		name       string
		firstParty bool
		visibility *entity.SeriesVisibility
	}
	public, unlisted := entity.SeriesVisibilityPublic, entity.SeriesVisibilityUnlisted
	seriesCases := []seriesCase{
		{"discovered", false, nil},
		{"first-party, public", true, &public},
		{"first-party, unlisted", true, &unlisted},
	}
	states := map[bool][]*entity.PublishState{
		false: {nil},
		true:  {new(entity.PublishStateDraft), new(entity.PublishStatePublished), new(entity.PublishStateCancelled)},
	}

	type seeded struct {
		event  *entity.Event
		series *entity.Series
	}
	byID := map[string]seeded{}
	newSeries := func(c seriesCase) (string, *entity.Series) {
		id := uuid.NewV7().String()
		var org, vis any
		series := &entity.Series{ID: id}
		if c.firstParty {
			org, vis = organizerID, string(*c.visibility)
			series.OrganizerID, series.Visibility = &organizerID, c.visibility
		}
		_, err := db.Pool.Exec(ctx, `INSERT INTO series (id, title, type, organizer_id, visibility) VALUES ($1, 'Parity', 'SINGLE', $2, $3)`, id, org, vis)
		require.NoError(t, err)
		return id, series
	}
	for _, c := range seriesCases {
		for _, state := range states[c.firstParty] {
			seriesID, series := newSeries(c)
			id := insertEvent(seriesID, state)
			byID[id] = seeded{event: &entity.Event{ID: id, SeriesID: seriesID, PublishState: state}, series: series}
		}
	}

	idsWhere := func(condition string) map[string]bool {
		rows, err := db.Pool.Query(ctx, `SELECT e.id FROM events e JOIN series s ON s.id = e.series_id WHERE `+condition)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]bool{}
		for rows.Next() {
			var id string
			require.NoError(t, rows.Scan(&id))
			out[id] = true
		}
		require.NoError(t, rows.Err())
		return out
	}

	fanVisible := idsWhere(rdb.FanVisibleCondition)
	catalog := idsWhere(rdb.CatalogEventCondition)
	for id, s := range byID {
		assert.Equal(t, s.event.IsPubliclyVisible(s.series), fanVisible[id], "fanVisibleCondition: %s series, event state %v", s.series.OrganizerID != nil, s.event.PublishState)
		assert.Equal(t, s.event.IsInCatalog(), catalog[id], "catalogEventCondition: event state %v", s.event.PublishState)
	}
	assert.Len(t, byID, 7, "every combination was seeded")

	t.Run("the derived publish state of a series", func(t *testing.T) {
		// Every non-empty set of event states of a first-party series, and a
		// discovered series.
		sets := [][]entity.PublishState{
			{entity.PublishStateDraft},
			{entity.PublishStatePublished},
			{entity.PublishStateCancelled},
			{entity.PublishStateDraft, entity.PublishStatePublished},
			{entity.PublishStateDraft, entity.PublishStateCancelled},
			{entity.PublishStatePublished, entity.PublishStateCancelled},
			{entity.PublishStateDraft, entity.PublishStatePublished, entity.PublishStateCancelled},
		}
		type want struct {
			firstParty bool
			events     []*entity.Event
		}
		wants := map[string]want{}
		for _, set := range sets {
			seriesID, _ := newSeries(seriesCase{firstParty: true, visibility: &public})
			var events []*entity.Event
			for _, st := range set {
				state := st
				events = append(events, &entity.Event{ID: insertEvent(seriesID, &state), PublishState: &state})
			}
			wants[seriesID] = want{true, events}
		}
		discoveredID, _ := newSeries(seriesCase{})
		wants[discoveredID] = want{false, []*entity.Event{{ID: insertEvent(discoveredID, nil)}}}

		rows, err := db.Pool.Query(ctx, `SELECT s.id, `+rdb.SeriesPublishStateColumn+` FROM series s WHERE s.id = ANY($1::uuid[])`, keysOf(wants))
		require.NoError(t, err)
		defer rows.Close()
		seen := 0
		for rows.Next() {
			var id string
			var got *string
			require.NoError(t, rows.Scan(&id, &got))
			w := wants[id]
			want := entity.DerivePublishState(w.firstParty, w.events)
			if want == nil {
				assert.Nil(t, got, "a discovered series has no state")
			} else if assert.NotNil(t, got) {
				assert.Equal(t, string(*want), *got)
			}
			seen++
		}
		require.NoError(t, rows.Err())
		assert.Equal(t, len(wants), seen)
	})
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
