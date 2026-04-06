-- Add listed_venue_name column to venues to store the raw scraped name alongside
-- the canonical Google Places name. This enables a DB-first lookup in the concert
-- creation pipeline, avoiding redundant Places API calls for known venues.
ALTER TABLE venues ADD COLUMN listed_venue_name TEXT;

-- Unique index on (listed_venue_name, admin_area) for efficient lookup.
-- NULLS NOT DISTINCT ensures two NULLs are treated as equal (same unknown venue).
CREATE UNIQUE INDEX idx_venues_listed_name_admin_area
    ON venues (listed_venue_name, admin_area) NULLS NOT DISTINCT
    WHERE listed_venue_name IS NOT NULL;
