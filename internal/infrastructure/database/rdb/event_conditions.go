package rdb

import (
	"database/sql"

	"github.com/liverty-music/backend/internal/entity"
)

// The SQL fragments below are the only places that read an event's publish
// state to decide what a reader sees (OpenSpec change
// restructure-event-publishing, design D4). Every query over events that is
// not a series lifecycle write uses them instead of its own predicate.
const (
	// notDraftPredicate is true for every event that is not DRAFT: a
	// discovered event (NULL), and a PUBLISHED or CANCELLED one. It is the
	// predicate of the partial unique index uq_events_natural_key, and an
	// upsert names it in ON CONFLICT (...) WHERE so PostgreSQL infers that
	// index.
	notDraftPredicate = `publish_state IS DISTINCT FROM 'DRAFT'`

	// catalogEventCondition keeps the events that are concerts: a DRAFT event
	// is not a concert, holds no slot and is never matched, merged, filled or
	// removed by a Concert operation (components/entity/concert "A draft event
	// is not a concert"). It expects events aliased as e.
	catalogEventCondition = `e.` + notDraftPredicate

	// fanVisibleCondition keeps the events visible to fans: a discovered
	// event, or a PUBLISHED event of a PUBLIC first-party series
	// (components/entity/event "Public visibility"). It expects events aliased
	// as e and their series as s.
	fanVisibleCondition = `(s.organizer_id IS NULL OR (e.publish_state = 'PUBLISHED' AND s.visibility = 'PUBLIC'))`

	// seriesPublishStateColumn derives a series' publish state from its
	// events: PUBLISHED when one is PUBLISHED, else CANCELLED when one is
	// CANCELLED, else DRAFT; NULL for a discovered series
	// (components/entity/series "Publish state is derived from the events").
	// It expects series aliased as s.
	seriesPublishStateColumn = `CASE
		WHEN s.organizer_id IS NULL THEN NULL
		WHEN EXISTS (SELECT 1 FROM events pe WHERE pe.series_id = s.id AND pe.publish_state = 'PUBLISHED') THEN 'PUBLISHED'
		WHEN EXISTS (SELECT 1 FROM events pe WHERE pe.series_id = s.id AND pe.publish_state = 'CANCELLED') THEN 'CANCELLED'
		ELSE 'DRAFT'
	END`
)

// nullPublishState maps a nullable publish_state column to the entity: nil for
// an event or series that has none.
func nullPublishState(v sql.NullString) *entity.PublishState {
	if !v.Valid {
		return nil
	}
	state := entity.PublishState(v.String)
	return &state
}
