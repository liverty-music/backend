-- Modify "users" table
ALTER TABLE "users" ALTER COLUMN "preferred_language" DROP DEFAULT;
-- Reset every non-NULL row so clients backfill via UpdatePreferredLanguage
-- on next observation. NULL is the canonical "not yet set by client" state
-- now that the DEFAULT is removed.
--
-- Safety: before this migration the column was never user-writable. No
-- proto field exposed it, no Update / UpdateHome query referenced it, and
-- no RPC accepted it as input. Every non-NULL value therefore originated
-- from the table DEFAULT (or out-of-band ops writes that pre-date the
-- product contract, which we deliberately want to discard so the client's
-- effective locale wins on next hydration). Going forward only the
-- dedicated UpdatePreferredLanguage RPC can write the column — the
-- generic Update query no longer touches it — so the no-explicit-write
-- guarantee continues to hold.
--
-- Using `IS NOT NULL` (rather than `= 'en'`) sidesteps the need to assert
-- that every existing value came from the DEFAULT specifically: even an
-- accidental ops write to a non-'en' value gets reset and backfilled
-- correctly by the client.
UPDATE "users" SET "preferred_language" = NULL WHERE "preferred_language" IS NOT NULL;
-- Set comment to column: "preferred_language" on table: "users"
COMMENT ON COLUMN "users"."preferred_language" IS 'User preferred language code (e.g., en, ja). NULL means "not yet set by client"; client backfills via UpdatePreferredLanguage on first observation.';
