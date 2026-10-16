package migrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventPublishStatePrevious is the last migration before
// 20261013000000_event_publish_state.sql.
const eventPublishStatePrevious = "20261012235000"

// eventPublishStateVersion is 20261013000000_event_publish_state.sql. The test
// stops there: its old-shape organizer predates the required short name.
const eventPublishStateVersion = "20261013000000"

// TestMigration_EventPublishState loads, in the old shape, a DRAFT series with
// two draft events and one draft performer, a PUBLISHED series whose event
// claimed a discovered event, a CANCELLED series with an event, a CANCELLED
// series with only draft rows, a PUBLISHED series with neither events nor
// draft rows, and a discovered event at the slot of one draft. It applies
// 20261013000000_event_publish_state.sql and reads the rows back with the
// publish state on each event.
func TestMigration_EventPublishState(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(eventPublishStatePrevious)

	var (
		venueID        = id(1)
		artistID       = id(2)
		organizerID    = id(3)
		draftSeries    = id(10)
		draftA         = id(11) // at the slot of the discovered event
		draftB         = id(12)
		publishedSer   = id(20)
		claimedEvent   = id(21)
		cancelledSer   = id(30)
		cancelledEvent = id(31)
		draftOnlyCanc  = id(40)
		draftOnlyRow   = id(41)
		emptyPublished = id(50)
		discoveredSer  = id(60)
		discoveredEv   = id(61)
	)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO venues (id, name) VALUES ($1, 'Zepp Haneda')`, venueID)
	mustExec(`INSERT INTO artists (id, name, mbid) VALUES ($1, 'The Band', '4a6b6e1c-1b4e-4e43-9e8b-2f6c3c1f0a01')`, artistID)
	mustExec(`INSERT INTO organizers (id, name, operator_email, status) VALUES ($1, 'E2E Test', 'ops@example.com', 1)`, organizerID)

	firstParty := func(seriesID, state string) {
		t.Helper()
		mustExec(`INSERT INTO series (id, title, type, organizer_id, visibility, publish_state)
		      VALUES ($1, 'Tour', 'TOUR', $2, 'PUBLIC', $3::series_publish_state)`, seriesID, organizerID, state)
	}
	firstParty(draftSeries, "DRAFT")
	firstParty(publishedSer, "PUBLISHED")
	firstParty(cancelledSer, "CANCELLED")
	firstParty(draftOnlyCanc, "CANCELLED")
	firstParty(emptyPublished, "PUBLISHED")
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Found Tour', 'TOUR')`, discoveredSer)

	liveEvent := func(eventID, seriesID, date string) {
		t.Helper()
		mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date, start_at)
		      VALUES ($1, $2, $3, $4::date, ($4::date + time '18:00') AT TIME ZONE 'Asia/Tokyo')`, eventID, seriesID, venueID, date)
		mustExec(`INSERT INTO concerts (event_id) VALUES ($1)`, eventID)
		mustExec(`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`, eventID, artistID)
	}
	liveEvent(claimedEvent, publishedSer, "2026-12-01")
	liveEvent(cancelledEvent, cancelledSer, "2026-12-02")
	liveEvent(discoveredEv, discoveredSer, "2026-12-03")

	draftEvent := func(eventID, seriesID, date string) {
		t.Helper()
		mustExec(`INSERT INTO draft_events (id, series_id, venue_id, listed_venue_name, local_event_date, start_at)
		      VALUES ($1, $2, $3, 'Zepp Haneda (TOKYO)', $4::date, ($4::date + time '18:00') AT TIME ZONE 'Asia/Tokyo')`,
			eventID, seriesID, venueID, date)
	}
	draftEvent(draftA, draftSeries, "2026-12-03")
	draftEvent(draftB, draftSeries, "2026-12-04")
	mustExec(`INSERT INTO draft_series_performers (series_id, artist_id) VALUES ($1, $2)`, draftSeries, artistID)
	draftEvent(draftOnlyRow, draftOnlyCanc, "2026-12-05")
	mustExec(`INSERT INTO draft_series_performers (series_id, artist_id) VALUES ($1, $2)`, draftOnlyCanc, artistID)

	apply(eventPublishStateVersion)

	stateOf := func(t *testing.T, eventID string) *string {
		t.Helper()
		var state *string
		require.NoError(t, conn.QueryRow(ctx, `SELECT publish_state::text FROM events WHERE id = $1`, eventID).Scan(&state))
		return state
	}
	seriesExists := func(t *testing.T, seriesID string) bool {
		t.Helper()
		var exists bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM series WHERE id = $1)`, seriesID).Scan(&exists))
		return exists
	}

	t.Run("draft ids become DRAFT events, each a concert with the draft performer", func(t *testing.T) {
		for _, eventID := range []string{draftA, draftB} {
			assert.Equal(t, "DRAFT", *stateOf(t, eventID))
			var listedName string
			var performers int
			require.NoError(t, conn.QueryRow(ctx, `
				SELECT e.listed_venue_name, (SELECT count(*) FROM concert_artists ca WHERE ca.event_id = c.event_id AND ca.artist_id = $2)
				FROM events e JOIN concerts c ON c.event_id = e.id
				WHERE e.id = $1 AND e.series_id = $3`, eventID, artistID, draftSeries).Scan(&listedName, &performers))
			assert.Equal(t, "Zepp Haneda (TOKYO)", listedName)
			assert.Equal(t, 1, performers)
		}
	})

	t.Run("published and cancelled events carry their series' state", func(t *testing.T) {
		assert.Equal(t, "PUBLISHED", *stateOf(t, claimedEvent))
		assert.Equal(t, "CANCELLED", *stateOf(t, cancelledEvent))
		assert.True(t, seriesExists(t, cancelledSer))
	})

	t.Run("a first-party series left with no event is gone", func(t *testing.T) {
		assert.False(t, seriesExists(t, draftOnlyCanc), "cancelled series with only draft rows")
		assert.False(t, seriesExists(t, emptyPublished), "published series with neither events nor draft rows")
		var draftLeft bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE id = $1)`, draftOnlyRow).Scan(&draftLeft))
		assert.False(t, draftLeft, "a draft of a cancelled series does not become an event")
	})

	t.Run("the discovered event keeps its slot and has no publish state", func(t *testing.T) {
		assert.Nil(t, stateOf(t, discoveredEv))
		assert.True(t, seriesExists(t, discoveredSer))
	})

	t.Run("a second PUBLISHED event at a taken slot is rejected while a second DRAFT is accepted", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		_, err = tx.Exec(ctx, `INSERT INTO events (id, series_id, venue_id, local_event_date, start_at, publish_state)
			SELECT $1, $2, venue_id, local_event_date, start_at, 'DRAFT' FROM events WHERE id = $3`, id(70), draftSeries, discoveredEv)
		require.NoError(t, err, "a second draft at the discovered event's slot")

		_, err = tx.Exec(ctx, `SAVEPOINT taken`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `INSERT INTO events (id, series_id, venue_id, local_event_date, start_at, publish_state)
			SELECT $1, $2, venue_id, local_event_date, start_at, 'PUBLISHED' FROM events WHERE id = $3`, id(71), publishedSer, discoveredEv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uq_events_natural_key")
	})

	t.Run("the draft tables and the stored series state are gone", func(t *testing.T) {
		var leftovers int
		require.NoError(t, conn.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM information_schema.tables WHERE table_schema = 'app' AND table_name IN ('draft_events', 'draft_series_performers'))
			     + (SELECT count(*) FROM information_schema.columns WHERE table_schema = 'app' AND table_name = 'series' AND column_name IN ('publish_state', 'published_at', 'cancelled_at'))
			     + (SELECT count(*) FROM pg_type WHERE typname = 'series_publish_state')`).Scan(&leftovers))
		assert.Zero(t, leftovers)
	})
}
