-- +goose Up
-- Add UNIQUE index on artists.mbid for ON CONFLICT deduplication.
-- Replaces the existing non-unique index. Only indexes non-empty MBIDs
-- since some external artists may lack a MusicBrainz ID.

DROP INDEX IF EXISTS idx_artists_mbid;

CREATE UNIQUE INDEX idx_artists_mbid ON artists (mbid) WHERE mbid IS NOT NULL AND mbid != '';
