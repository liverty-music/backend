-- Modify "venues" table
ALTER TABLE "venues" ALTER COLUMN "admin_area" SET NOT NULL, ALTER COLUMN "time_zone" SET NOT NULL, ALTER COLUMN "country_code" SET NOT NULL;
-- Set comment to column: "admin_area" on table: "venues"
COMMENT ON COLUMN "venues"."admin_area" IS 'Code of the first-level Area of the venue (e.g., JP-13, HK)';
-- Set comment to column: "time_zone" on table: "venues"
COMMENT ON COLUMN "venues"."time_zone" IS 'IANA time zone of the venue, in which its events'' wall-clock times are read; one of its country''s zones, equal to the Area''s zone when the Area has one';
