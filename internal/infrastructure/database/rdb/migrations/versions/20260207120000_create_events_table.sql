-- Create events table
CREATE TABLE events (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    local_event_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    open_at TIMESTAMPTZ,
    source_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Migrate data from concerts to events
INSERT INTO events (id, venue_id, title, local_event_date, start_at, open_at, source_url, created_at, updated_at)
SELECT
    id,
    venue_id,
    title,
    local_event_date,
    start_time,
    open_time,
    source_url,
    created_at,
    updated_at
FROM concerts;

-- Drop generic columns from concerts
ALTER TABLE concerts
    DROP COLUMN venue_id,
    DROP COLUMN title,
    DROP COLUMN local_event_date,
    DROP COLUMN start_time,
    DROP COLUMN open_time,
    DROP COLUMN source_url,
    DROP COLUMN created_at,
    DROP COLUMN updated_at;

-- Rename id to event_id and add constraints
ALTER TABLE concerts
    RENAME COLUMN id TO event_id;

ALTER TABLE concerts
    ADD CONSTRAINT fk_concerts_events FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE;

-- Create indexes for events
CREATE INDEX idx_events_local_event_date ON events(local_event_date);
CREATE INDEX idx_events_venue_id ON events(venue_id);

-- Drop old index on concerts (if name conflict or cleanup needed)
DROP INDEX IF EXISTS idx_concerts_date;
