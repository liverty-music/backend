-- Add venue enrichment fields for the venue normalization pipeline.
-- Tracks enrichment status and stores external identifiers from MusicBrainz and Google Maps.

-- Create enrichment status enum type
CREATE TYPE venue_enrichment_status AS ENUM ('pending', 'enriched', 'failed');

-- Add enrichment columns to venues table
ALTER TABLE venues ADD COLUMN mbid TEXT;
ALTER TABLE venues ADD COLUMN google_place_id TEXT;
ALTER TABLE venues ADD COLUMN enrichment_status venue_enrichment_status NOT NULL DEFAULT 'pending';
ALTER TABLE venues ADD COLUMN raw_name TEXT;

-- Backfill raw_name from existing name
UPDATE venues SET raw_name = name;

-- Add unique partial indexes for external identifiers
CREATE UNIQUE INDEX idx_venues_mbid ON venues (mbid) WHERE mbid IS NOT NULL;
CREATE UNIQUE INDEX idx_venues_google_place_id ON venues (google_place_id) WHERE google_place_id IS NOT NULL;

-- Add non-unique index for raw_name lookups (used in GetByName fallback)
CREATE INDEX idx_venues_raw_name ON venues (raw_name);

COMMENT ON COLUMN venues.mbid IS 'MusicBrainz Place ID (UUID format) for the canonical venue record; NULL until enriched';
COMMENT ON COLUMN venues.google_place_id IS 'Google Maps Place ID for the canonical venue record; NULL until enriched';
COMMENT ON COLUMN venues.enrichment_status IS 'Current state of the venue normalization pipeline: pending (default), enriched, or failed';
COMMENT ON COLUMN venues.raw_name IS 'Original scraper-provided venue name before canonical renaming; backfilled from name on migration';
