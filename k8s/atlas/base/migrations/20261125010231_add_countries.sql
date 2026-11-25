-- Create "countries" table
CREATE TABLE "countries" (
  "code" text NOT NULL,
  "name_en" text NOT NULL,
  "name_ja" text NULL,
  PRIMARY KEY ("code"),
  CONSTRAINT "chk_countries_code" CHECK (code ~ '^[A-Z]{2}$'::text)
);
-- Set comment to table: "countries"
COMMENT ON TABLE "countries" IS 'Countries and territories that have first-level areas, with their CLDR territory names, for the country select of the organizer console';
-- Set comment to column: "code" on table: "countries"
COMMENT ON COLUMN "countries"."code" IS 'ISO 3166-1 alpha-2 country or territory code (e.g., KR)';
-- Set comment to column: "name_en" on table: "countries"
COMMENT ON COLUMN "countries"."name_en" IS 'CLDR English territory name (e.g., South Korea)';
-- Set comment to column: "name_ja" on table: "countries"
COMMENT ON COLUMN "countries"."name_ja" IS 'CLDR Japanese territory name (e.g., 韓国); NULL when CLDR has none';
