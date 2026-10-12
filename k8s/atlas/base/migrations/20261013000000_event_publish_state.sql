-- Move the publish state from the Series onto each Event (OpenSpec change
-- restructure-event-publishing, design D1-D3 and "Migration Plan" step 3).
--
-- A draft becomes an ordinary event in the DRAFT state, so draft_events and
-- draft_series_performers fold into events, concerts and concert_artists. The
-- natural key becomes a partial unique index that skips DRAFT rows, and the
-- Series loses its stored state, which is now derived from its events.
-- Forward-only: production holds only test data (counted read-only on
-- 2026-10-12, design "Migration Plan" step 1).

SET search_path TO app, public;

-- 1. The per-event publish state.
CREATE TYPE event_publish_state AS ENUM ('DRAFT', 'PUBLISHED', 'CANCELLED');
COMMENT ON TYPE event_publish_state IS 'Publish lifecycle of an event of a first-party series: DRAFT (console-only, holds no slot), PUBLISHED (live), CANCELLED (terminal, keeps its slot). NULL for an event of a discovered series.';

ALTER TABLE events ADD COLUMN publish_state event_publish_state;
COMMENT ON COLUMN events.publish_state IS 'Publish lifecycle of an event of a first-party series (DRAFT / PUBLISHED / CANCELLED). NULL exactly for an event of a discovered series; the series repository, the only writer of first-party events, always sets it. A DRAFT event is not a concert and holds no slot of uq_events_natural_key.';

-- 2. Each first-party series' state onto its live events. A DRAFT series has
-- no rows in events (its performances are in draft_events), so only PUBLISHED
-- and CANCELLED are copied here.
UPDATE events e
SET publish_state = s.publish_state::text::event_publish_state
FROM series s
WHERE s.id = e.series_id
  AND s.organizer_id IS NOT NULL;

-- 3. The natural key skips DRAFT rows.
ALTER TABLE events DROP CONSTRAINT uq_events_natural_key;
CREATE UNIQUE INDEX uq_events_natural_key ON events (venue_id, local_event_date, start_at) NULLS NOT DISTINCT
    WHERE publish_state IS DISTINCT FROM 'DRAFT';
COMMENT ON INDEX uq_events_natural_key IS 'Physical identity of a performance: one row per (venue, local date, start time) among events that are not DRAFT, independent of series or performing artist. start_at is part of the key so two shows at one venue on one date with different start times (matinee/evening) are distinct; NULLS NOT DISTINCT collapses two shows whose start time is not yet published. The same physical show discovered via different artists/series resolves to one row. DRAFT events are excluded, so any number of drafts and one live or cancelled event can share a slot. Upserts infer it with ON CONFLICT (venue_id, local_event_date, start_at) WHERE publish_state IS DISTINCT FROM ''DRAFT''.';

-- 4. Draft performances of DRAFT series become DRAFT events with their ids
-- kept, each a concert performed by the series' draft performers.
INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date, start_at, open_at, publish_state)
SELECT de.id, de.series_id, de.venue_id, de.listed_venue_name, de.local_event_date, de.start_at, de.open_at, 'DRAFT'
FROM draft_events de JOIN series s ON s.id = de.series_id
WHERE s.publish_state = 'DRAFT';

INSERT INTO concerts (event_id)
SELECT de.id FROM draft_events de JOIN series s ON s.id = de.series_id
WHERE s.publish_state = 'DRAFT';

INSERT INTO concert_artists (event_id, artist_id)
SELECT de.id, dsp.artist_id
FROM draft_events de
JOIN series s ON s.id = de.series_id
JOIN draft_series_performers dsp ON dsp.series_id = de.series_id
WHERE s.publish_state = 'DRAFT';

-- 5. A first-party series left with no event is removed: a CANCELLED series
-- with only draft rows (they cascade), and a series with neither events nor
-- draft rows (decided with the owner on 2026-10-12).
DELETE FROM series s
WHERE s.organizer_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.series_id = s.id);

-- 6. Drop the draft tables and the stored series state.
DROP TABLE draft_series_performers;
DROP TABLE draft_events;
DROP INDEX idx_series_first_party_state;
ALTER TABLE series DROP CONSTRAINT chk_series_first_party_state;
ALTER TABLE series DROP COLUMN publish_state, DROP COLUMN published_at, DROP COLUMN cancelled_at;
ALTER TABLE series ADD CONSTRAINT chk_series_first_party_state CHECK (
    (organizer_id IS NULL AND visibility IS NULL) OR
    (organizer_id IS NOT NULL AND visibility IS NOT NULL)
);
DROP TYPE series_publish_state;
