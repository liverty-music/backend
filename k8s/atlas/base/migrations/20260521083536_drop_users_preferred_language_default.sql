-- Modify "users" table
ALTER TABLE "users" ALTER COLUMN "preferred_language" DROP DEFAULT;
-- Reset rows seeded by the old DEFAULT 'en' so clients backfill via
-- UpdatePreferredLanguage on next observation. NULL is the canonical
-- "not yet set by client" state now that the DEFAULT is removed.
--
-- Safety: before this migration, NO code path wrote 'en' explicitly.
-- preferred_language was only ever populated by the table DEFAULT — the
-- field was not exposed on the proto User entity, no Update/UpdateHome
-- query referenced the column, and no RPC accepted it as input. Every
-- 'en' value in the table therefore originated from the DEFAULT and is
-- safe to coerce to NULL. (After this PR ships, the Update method is
-- also changed to NOT touch preferred_language — only the dedicated
-- UpdatePreferredLanguage RPC can write to the column, so the same
-- safety property holds going forward.)
UPDATE "users" SET "preferred_language" = NULL WHERE "preferred_language" = 'en';
-- Set comment to column: "preferred_language" on table: "users"
COMMENT ON COLUMN "users"."preferred_language" IS 'User preferred language code (e.g., en, ja). NULL means "not yet set by client"; client backfills via UpdatePreferredLanguage on first observation.';
