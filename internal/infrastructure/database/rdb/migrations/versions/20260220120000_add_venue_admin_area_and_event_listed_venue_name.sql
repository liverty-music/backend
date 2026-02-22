-- +goose Up
-- Add admin_area to venues for location-based dashboard filtering.
-- Add listed_venue_name to events to preserve the raw venue name as scraped.
ALTER TABLE venues ADD COLUMN admin_area TEXT;
ALTER TABLE events ADD COLUMN listed_venue_name TEXT;

COMMENT ON COLUMN venues.admin_area IS 'Administrative area (prefecture/state/province) where the venue is located. Populated when determinable with confidence from the source data; NULL if uncertain.';
COMMENT ON COLUMN events.listed_venue_name IS 'Raw venue name as listed on the artist official site or source page. Preserved for future normalization against Maps/MusicBrainz.';
