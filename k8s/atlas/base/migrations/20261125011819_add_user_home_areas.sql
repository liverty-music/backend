-- Create "user_home_areas" table
CREATE TABLE "user_home_areas" (
  "user_id" uuid NOT NULL,
  "area_code" text NOT NULL,
  "area_first_level" boolean NOT NULL DEFAULT true,
  PRIMARY KEY ("user_id"),
  CONSTRAINT "fk_user_home_areas_area" FOREIGN KEY ("area_code", "area_first_level") REFERENCES "areas" ("code", "is_first_level") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "user_home_areas_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "user_home_areas_area_first_level_check" CHECK (area_first_level)
);
-- Set comment to table: "user_home_areas"
COMMENT ON TABLE "user_home_areas" IS 'The home Area of each user: the first-level Area where the user attends live events without considering it a trip. One row per user; deleted with the user';
-- Set comment to column: "user_id" on table: "user_home_areas"
COMMENT ON COLUMN "user_home_areas"."user_id" IS 'The user whose home this is';
-- Set comment to column: "area_code" on table: "user_home_areas"
COMMENT ON COLUMN "user_home_areas"."area_code" IS 'Code of the first-level Area of the home (e.g., JP-13, HK)';
-- Set comment to column: "area_first_level" on table: "user_home_areas"
COMMENT ON COLUMN "user_home_areas"."area_first_level" IS 'Always true; with area_code it lets the foreign key require a first-level Area';
-- Copy each user's home from homes (OpenSpec change globalize-venues, task
-- 4.1). Only homes a user references are copied, so the rows a deleted user
-- left behind stay in homes until it is dropped. A home whose area is no
-- first-level Area (CN-TW, which has no row) is not copied: its fan picks a
-- home again.
INSERT INTO "user_home_areas" ("user_id", "area_code")
SELECT u.id, h.level_1
FROM "users" u
JOIN "homes" h ON h.id = u.home_id
JOIN "areas" a ON a.code = h.level_1 AND a.is_first_level;
