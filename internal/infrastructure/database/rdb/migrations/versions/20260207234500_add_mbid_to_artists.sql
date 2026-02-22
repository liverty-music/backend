-- +goose Up
-- Add mbid column to artists table
-- This allows for canonical artist identification via MusicBrainz.

ALTER TABLE artists ADD COLUMN mbid VARCHAR(36);

CREATE INDEX IF NOT EXISTS idx_artists_mbid ON artists(mbid);

COMMENT ON COLUMN artists.mbid IS 'Canonical MusicBrainz Identifier (MBID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)';
