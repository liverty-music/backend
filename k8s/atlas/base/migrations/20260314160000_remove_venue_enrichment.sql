-- Drop enrichment-related indexes
DROP INDEX IF EXISTS "idx_venues_mbid";
DROP INDEX IF EXISTS "idx_venues_raw_name";

-- Drop enrichment-related constraint
ALTER TABLE "venues" DROP CONSTRAINT IF EXISTS "chk_venues_raw_name_not_empty";

-- Drop enrichment columns
ALTER TABLE "venues" DROP COLUMN IF EXISTS "mbid";
ALTER TABLE "venues" DROP COLUMN IF EXISTS "enrichment_status";
ALTER TABLE "venues" DROP COLUMN IF EXISTS "raw_name";

-- Drop enrichment enum type
DROP TYPE IF EXISTS "venue_enrichment_status";

-- Update column comments
COMMENT ON COLUMN venues.name IS 'Canonical venue name from Google Places API';
COMMENT ON COLUMN venues.google_place_id IS 'Google Maps Place ID for the canonical venue record';
COMMENT ON COLUMN venues.latitude IS 'WGS 84 latitude of the venue from Google Places API';
COMMENT ON COLUMN venues.longitude IS 'WGS 84 longitude of the venue from Google Places API';
