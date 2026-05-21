-- Modify "users" table
ALTER TABLE "users" ALTER COLUMN "preferred_language" DROP DEFAULT;
-- Reset rows that were seeded by the old DEFAULT 'en' so clients can backfill
-- via UpdatePreferredLanguage on next observation. NULL is the canonical
-- "not yet set by client" state now that the DEFAULT is removed.
UPDATE "users" SET "preferred_language" = NULL WHERE "preferred_language" = 'en';
-- Set comment to column: "preferred_language" on table: "users"
COMMENT ON COLUMN "users"."preferred_language" IS 'User preferred language code (e.g., en, ja). NULL means "not yet set by client"; client backfills via UpdatePreferredLanguage on first observation.';
