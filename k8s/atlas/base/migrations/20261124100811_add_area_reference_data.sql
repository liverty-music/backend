SET search_path TO app, public;

-- Create "time_zones" table
CREATE TABLE "time_zones" (
  "name" text NOT NULL,
  "name_en" text NOT NULL,
  "name_ja" text NULL,
  PRIMARY KEY ("name")
);
-- Set comment to table: "time_zones"
COMMENT ON TABLE "time_zones" IS 'IANA time zones a venue or ticket sale can have, with the names a screen shows for them';
-- Set comment to column: "name" on table: "time_zones"
COMMENT ON COLUMN "time_zones"."name" IS 'IANA time zone name (e.g., Asia/Seoul)';
-- Set comment to column: "name_en" on table: "time_zones"
COMMENT ON COLUMN "time_zones"."name_en" IS 'English display name, the CLDR exemplar city (e.g., Seoul) unless manual_time_zone_names.csv overrides it';
-- Set comment to column: "name_ja" on table: "time_zones"
COMMENT ON COLUMN "time_zones"."name_ja" IS 'Japanese display name (e.g., ソウル); NULL when the reference data has none';
-- Create "country_time_zones" table
CREATE TABLE "country_time_zones" (
  "country_code" text NOT NULL,
  "time_zone" text NOT NULL,
  PRIMARY KEY ("country_code", "time_zone"),
  CONSTRAINT "country_time_zones_time_zone_fkey" FOREIGN KEY ("time_zone") REFERENCES "time_zones" ("name") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "chk_country_time_zones_country_code" CHECK (country_code ~ '^[A-Z]{2}$'::text)
);
-- Set comment to table: "country_time_zones"
COMMENT ON TABLE "country_time_zones" IS 'The time zones that venues in a country can have, from the IANA zone.tab';
-- Set comment to column: "country_code" on table: "country_time_zones"
COMMENT ON COLUMN "country_time_zones"."country_code" IS 'ISO 3166-1 alpha-2 country or territory code (e.g., KR)';
-- Set comment to column: "time_zone" on table: "country_time_zones"
COMMENT ON COLUMN "country_time_zones"."time_zone" IS 'IANA time zone name of one of the country''s zones';
-- Create "areas" table
CREATE TABLE "areas" (
  "code" text NOT NULL,
  "kind" text NOT NULL,
  "country_code" text NOT NULL,
  "parent_code" text NULL,
  "replaced_by_code" text NULL,
  "name_en" text NOT NULL,
  "name_ja" text NULL,
  "reference_latitude" double precision NULL,
  "reference_longitude" double precision NULL,
  "reference_point_source" text NULL,
  "time_zone" text NULL,
  "is_first_level" boolean NULL GENERATED ALWAYS AS ((kind <> 'region'::text) AND (parent_code IS NULL) AND (replaced_by_code IS NULL)) STORED,
  PRIMARY KEY ("code"),
  CONSTRAINT "uq_areas_code_first_level" UNIQUE ("code", "is_first_level"),
  CONSTRAINT "areas_parent_code_fkey" FOREIGN KEY ("parent_code") REFERENCES "areas" ("code") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "areas_replaced_by_code_fkey" FOREIGN KEY ("replaced_by_code") REFERENCES "areas" ("code") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "fk_areas_time_zone" FOREIGN KEY ("country_code", "time_zone") REFERENCES "country_time_zones" ("country_code", "time_zone") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "chk_areas_code" CHECK (((kind = 'subdivision'::text) AND (code ~ '^[A-Z]{2}-[A-Z0-9]{1,3}$'::text)) OR ((kind = 'country'::text) AND (code ~ '^[A-Z]{2}$'::text)) OR ((kind = 'region'::text) AND (code ~ '^[A-Z]{2}-[A-Z]{4,}$'::text))),
  CONSTRAINT "chk_areas_country" CHECK ("left"(code, 2) = country_code),
  CONSTRAINT "chk_areas_kind" CHECK (kind = ANY (ARRAY['subdivision'::text, 'country'::text, 'region'::text])),
  CONSTRAINT "chk_areas_reference_point" CHECK (((reference_latitude IS NULL) = (reference_longitude IS NULL)) AND ((reference_latitude IS NULL) = (reference_point_source IS NULL)))
);
-- Create index "idx_areas_name_en" to table: "areas"
CREATE INDEX "idx_areas_name_en" ON "areas" ((lower(name_en)));
-- Create index "idx_areas_name_ja" to table: "areas"
CREATE INDEX "idx_areas_name_ja" ON "areas" ("name_ja");
-- Set comment to table: "areas"
COMMENT ON TABLE "areas" IS 'Places named by a standard code: first-level subdivisions, countries without subdivisions, regions that group first-level areas, and lower-level or replaced codes kept only so a code can be resolved to its first-level area';
-- Set comment to column: "code" on table: "areas"
COMMENT ON COLUMN "areas"."code" IS 'The area code: ISO 3166-2 (JP-13), ISO 3166-1 for a country without subdivisions (HK), or a region (JP-KANTO, suffix of four or more letters)';
-- Set comment to column: "kind" on table: "areas"
COMMENT ON COLUMN "areas"."kind" IS 'subdivision, country or region';
-- Set comment to column: "country_code" on table: "areas"
COMMENT ON COLUMN "areas"."country_code" IS 'ISO 3166-1 alpha-2 code of the country the area belongs to; the first two letters of the code';
-- Set comment to column: "parent_code" on table: "areas"
COMMENT ON COLUMN "areas"."parent_code" IS 'The area that contains this one; NULL for a first-level area and for a region';
-- Set comment to column: "replaced_by_code" on table: "areas"
COMMENT ON COLUMN "areas"."replaced_by_code" IS 'The area that replaced this code; NULL while the code is current';
-- Set comment to column: "name_en" on table: "areas"
COMMENT ON COLUMN "areas"."name_en" IS 'English name';
-- Set comment to column: "name_ja" on table: "areas"
COMMENT ON COLUMN "areas"."name_ja" IS 'Japanese name; NULL when the reference data has none';
-- Set comment to column: "reference_latitude" on table: "areas"
COMMENT ON COLUMN "areas"."reference_latitude" IS 'WGS 84 latitude of the reference point proximity is measured from; NULL when the area has none';
-- Set comment to column: "reference_longitude" on table: "areas"
COMMENT ON COLUMN "areas"."reference_longitude" IS 'WGS 84 longitude of the reference point proximity is measured from; NULL when the area has none';
-- Set comment to column: "reference_point_source" on table: "areas"
COMMENT ON COLUMN "areas"."reference_point_source" IS 'Where the reference point comes from: legacy_prefecture_table, natural_earth_label, natural_earth_children or manual; NULL when the area has no reference point';
-- Set comment to column: "time_zone" on table: "areas"
COMMENT ON COLUMN "areas"."time_zone" IS 'The IANA time zone of the whole area; NULL when the area spans several zones or the reference data names none';
-- Set comment to column: "is_first_level" on table: "areas"
COMMENT ON COLUMN "areas"."is_first_level" IS 'True for a current subdivision or country that has no parent: the only areas a venue or a home can name. Stored so that foreign keys can require it';
-- Set comment to index: "idx_areas_name_en" on table: "areas"
COMMENT ON INDEX "idx_areas_name_en" IS 'Case-insensitive lookup of an area by its English name in Area.Resolve';
-- Set comment to index: "idx_areas_name_ja" on table: "areas"
COMMENT ON INDEX "idx_areas_name_ja" IS 'Lookup of an area by its Japanese name in Area.Resolve';
-- Create "area_memberships" table
CREATE TABLE "area_memberships" (
  "region_code" text NOT NULL,
  "member_code" text NOT NULL,
  "member_first_level" boolean NOT NULL DEFAULT true,
  PRIMARY KEY ("region_code", "member_code"),
  CONSTRAINT "area_memberships_region_code_fkey" FOREIGN KEY ("region_code") REFERENCES "areas" ("code") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "fk_area_memberships_member" FOREIGN KEY ("member_code", "member_first_level") REFERENCES "areas" ("code", "is_first_level") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "area_memberships_member_first_level_check" CHECK (member_first_level)
);
-- Set comment to table: "area_memberships"
COMMENT ON TABLE "area_memberships" IS 'The first-level areas each region groups (JP-KANTO contains JP-13); a region is a grouping, not an administrative parent';
-- Set comment to column: "region_code" on table: "area_memberships"
COMMENT ON COLUMN "area_memberships"."region_code" IS 'The region';
-- Set comment to column: "member_code" on table: "area_memberships"
COMMENT ON COLUMN "area_memberships"."member_code" IS 'A first-level area the region contains';
-- Set comment to column: "member_first_level" on table: "area_memberships"
COMMENT ON COLUMN "area_memberships"."member_first_level" IS 'Always true; with member_code it lets the foreign key require a first-level member';
