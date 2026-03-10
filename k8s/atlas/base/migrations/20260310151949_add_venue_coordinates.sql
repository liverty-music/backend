-- Modify "venues" table
ALTER TABLE "venues" ADD COLUMN "latitude" double precision NULL, ADD COLUMN "longitude" double precision NULL;
-- Set comment to column: "latitude" on table: "venues"
COMMENT ON COLUMN "venues"."latitude" IS 'WGS 84 latitude of the venue, populated during enrichment from MusicBrainz or Google Places; NULL until enriched';
-- Set comment to column: "longitude" on table: "venues"
COMMENT ON COLUMN "venues"."longitude" IS 'WGS 84 longitude of the venue, populated during enrichment from MusicBrainz or Google Places; NULL until enriched';
