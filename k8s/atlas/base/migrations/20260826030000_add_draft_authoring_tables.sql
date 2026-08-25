-- Add the first-party authoring draft tables (organizer-event-authoring).
--
-- While a first-party series is a DRAFT, its performances and performers are
-- held out of the live "events" / "event_performers" tables so the natural-key
-- space (uq_events_natural_key) stays clean and no discovered slot is claimed
-- before publish. On publish each draft event is materialized into "events" via
-- the natural-key upsert (with the supersede / suppression / cross-organizer
-- rules) and draft performers become event_performers on every materialized
-- event. Both tables cascade on series delete.

CREATE TABLE IF NOT EXISTS draft_events (
    id UUID PRIMARY KEY,
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    listed_venue_name TEXT,
    local_event_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    open_at TIMESTAMPTZ,
    CONSTRAINT chk_draft_events_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE draft_events IS 'Unpublished performances of a first-party DRAFT series, held out of the live "events" table until publish. No natural-key uniqueness so drafts are freely editable.';
COMMENT ON COLUMN draft_events.id IS 'Unique draft-event identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN draft_events.series_id IS 'Parent first-party series being authored';
COMMENT ON COLUMN draft_events.venue_id IS 'Venue resolved (Places get-or-create) at draft time';
COMMENT ON COLUMN draft_events.listed_venue_name IS 'Raw venue name the organizer entered, preserved for display and re-resolution';
COMMENT ON COLUMN draft_events.local_event_date IS 'Date of the performance';
COMMENT ON COLUMN draft_events.start_at IS 'Performance start time (absolute), if set';
COMMENT ON COLUMN draft_events.open_at IS 'Doors open time (absolute), if set';

CREATE INDEX idx_draft_events_series_id ON draft_events(series_id);
COMMENT ON INDEX idx_draft_events_series_id IS 'Optimizes loading all draft events of a series';

CREATE TABLE IF NOT EXISTS draft_series_performers (
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    PRIMARY KEY (series_id, artist_id)
);

COMMENT ON TABLE draft_series_performers IS 'Series-level performers of a first-party DRAFT series, materialized into event_performers for every event on publish.';
COMMENT ON COLUMN draft_series_performers.series_id IS 'Parent first-party series being authored';
COMMENT ON COLUMN draft_series_performers.artist_id IS 'A performing artist the organizer represents';
