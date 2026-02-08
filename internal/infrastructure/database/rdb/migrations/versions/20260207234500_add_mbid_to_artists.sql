-- Add mbid column to artists table
-- This allows for canonical artist identification via MusicBrainz.

ALTER TABLE artists ADD COLUMN mbid UUID NOT NULL UNIQUE;

CREATE INDEX IF NOT EXISTS idx_artists_mbid ON artists(mbid);

COMMENT ON COLUMN artists.mbid IS 'Canonical MusicBrainz Identifier (UUID)';
