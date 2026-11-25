-- Modify "concert_candidates" table
ALTER TABLE "concert_candidates" ADD COLUMN "time_zone" text NULL;
-- Set comment to column: "time_zone" on table: "concert_candidates"
COMMENT ON COLUMN "concert_candidates"."time_zone" IS 'IANA time zone of the venue as discovery resolved it: the Area''s zone, else the source''s when it is one of the country''s zones; NULL when neither is known';
-- Modify "rejected_concerts" table
ALTER TABLE "rejected_concerts" ADD COLUMN "time_zone" text NULL;
-- Set comment to column: "time_zone" on table: "rejected_concerts"
COMMENT ON COLUMN "rejected_concerts"."time_zone" IS 'IANA time zone of the venue as discovery resolved it, kept from the candidate; NULL when it had none';
-- Complete the venues' areas and time zones (OpenSpec change
-- globalize-venues, Decision 16 step 1). The columns come first, then the
-- data, then the constraints, so the constraints hold for every row.
ALTER TABLE "venues" ADD COLUMN "admin_area_first_level" boolean NOT NULL DEFAULT true, ADD COLUMN "time_zone" text NULL, ADD COLUMN "country_code" text NULL GENERATED ALWAYS AS ("left"(admin_area, 2)) STORED, ADD COLUMN "coordinates_fetch_time" timestamptz NULL;

-- A lower-level or withdrawn code becomes the first-level Area it leads to,
-- following the replacement and then the parents (FR-33 -> FR-NAQ, FR-75 ->
-- FR-IDF). Production had 10 such codes on 15 venues on 2026-11-25.
WITH RECURSIVE chain AS (
  SELECT a.code AS start, a.code, a.replaced_by_code, a.parent_code, a.is_first_level, 0 AS step
  FROM "areas" a
  WHERE a.kind <> 'region' AND NOT a.is_first_level
    AND a.code IN (SELECT DISTINCT admin_area FROM "venues" WHERE admin_area IS NOT NULL)
  UNION ALL
  SELECT c.start, a.code, a.replaced_by_code, a.parent_code, a.is_first_level, c.step + 1
  FROM chain c JOIN "areas" a ON a.code = COALESCE(c.replaced_by_code, c.parent_code)
  WHERE NOT c.is_first_level AND c.step < 6
)
UPDATE "venues" v SET admin_area = c.code
FROM chain c
WHERE c.is_first_level AND v.admin_area = c.start;

-- A code with no first-level Area (an omitted one such as CN-TW, or one no
-- Area has) is cleared; the backfill finds the venue's area again.
UPDATE "venues" SET admin_area = NULL
WHERE admin_area IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM "areas" a WHERE a.code = venues.admin_area AND a.is_first_level);

-- A venue takes its Area's zone when the whole Area has one.
UPDATE "venues" v SET time_zone = a.time_zone
FROM "areas" a
WHERE a.code = v.admin_area AND a.time_zone IS NOT NULL;

-- Existing coordinates count as fetched 25 days ago: the first refresh run
-- (which reads coordinates older than 20 days) fetches them again, and any it
-- cannot refresh expire 5 days later.
UPDATE "venues" SET coordinates_fetch_time = now() - interval '25 days' WHERE latitude IS NOT NULL;

-- A venue's name is no longer taken from the map catalog: a venue with a
-- place and a listed name is named by its listed name.
UPDATE "venues" SET name = listed_venue_name
WHERE google_place_id IS NOT NULL AND listed_venue_name IS NOT NULL AND listed_venue_name <> '';

ALTER TABLE "venues" ADD CONSTRAINT "chk_venues_admin_area_first_level" CHECK (admin_area_first_level), ADD CONSTRAINT "chk_venues_coordinates_fetch_time" CHECK ((latitude IS NULL) = (coordinates_fetch_time IS NULL)), ADD CONSTRAINT "fk_venues_admin_area" FOREIGN KEY ("admin_area", "admin_area_first_level") REFERENCES "areas" ("code", "is_first_level") ON UPDATE NO ACTION ON DELETE NO ACTION, ADD CONSTRAINT "fk_venues_time_zone" FOREIGN KEY ("country_code", "time_zone") REFERENCES "country_time_zones" ("country_code", "time_zone") ON UPDATE NO ACTION ON DELETE NO ACTION;
-- Set comment to column: "name" on table: "venues"
COMMENT ON COLUMN "venues"."name" IS 'Venue name: the name its source listed, or the name a member saved; never a name read from the map catalog unsaved';
-- Set comment to column: "admin_area" on table: "venues"
COMMENT ON COLUMN "venues"."admin_area" IS 'Code of the first-level Area of the venue (e.g., JP-13, HK); NULL only for a venue whose area is not known yet';
-- Set comment to column: "google_place_id" on table: "venues"
COMMENT ON COLUMN "venues"."google_place_id" IS 'Google Maps Place ID of the venue; kept indefinitely';
-- Set comment to column: "latitude" on table: "venues"
COMMENT ON COLUMN "venues"."latitude" IS 'WGS 84 latitude read from the map catalog at coordinates_fetch_time; cleared after 30 days';
-- Set comment to column: "longitude" on table: "venues"
COMMENT ON COLUMN "venues"."longitude" IS 'WGS 84 longitude read from the map catalog at coordinates_fetch_time; cleared after 30 days';
-- Set comment to column: "admin_area_first_level" on table: "venues"
COMMENT ON COLUMN "venues"."admin_area_first_level" IS 'Always true; with admin_area it lets the foreign key require a first-level Area';
-- Set comment to column: "time_zone" on table: "venues"
COMMENT ON COLUMN "venues"."time_zone" IS 'IANA time zone of the venue, in which its events'' wall-clock times are read; one of its country''s zones, equal to the Area''s zone when the Area has one; NULL only while not known yet';
-- Set comment to column: "country_code" on table: "venues"
COMMENT ON COLUMN "venues"."country_code" IS 'Country of the venue, the first two letters of admin_area; with time_zone it lets the foreign key require a zone of that country';
-- Set comment to column: "coordinates_fetch_time" on table: "venues"
COMMENT ON COLUMN "venues"."coordinates_fetch_time" IS 'When the coordinates were read from the map catalog; set exactly when they are';
