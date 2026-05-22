-- Modify "users" table
ALTER TABLE "users" ALTER COLUMN "preferred_language" DROP DEFAULT;
-- Reset every non-NULL row so clients backfill via UpdatePreferredLanguage
-- on next observation. NULL is the canonical "not yet set by client" state
-- now that the DEFAULT is removed.
--
-- Why this is safe to do unconditionally:
--   * Before this PR the column had a `DEFAULT 'en'`, so every existing
--     row's value is most likely default-seeded.
--   * On paper the old `UserRepository.Update()` did include
--     `preferred_language = $5` in its SET clause, so a sync path COULD
--     have written a non-default value. In practice that code path had
--     no production callers (verified by grep across handlers / webhooks
--     / event consumers) and no RPC ever exposed the field to clients.
--   * Even if some value did originate from an out-of-band ops write or
--     the Update path, blanket-resetting it is the desired outcome: the
--     client's currently effective locale wins on next hydration via
--     UpdatePreferredLanguage, restoring the user's true preference
--     without us having to guess which historical values to trust.
--
-- Going forward only the dedicated UpdatePreferredLanguage RPC can write
-- the column — the generic Update query no longer touches it — so the
-- no-explicit-write guarantee will hold without ambiguity from this PR
-- onward.
UPDATE "users" SET "preferred_language" = NULL WHERE "preferred_language" IS NOT NULL;
-- Set comment to column: "preferred_language" on table: "users"
COMMENT ON COLUMN "users"."preferred_language" IS 'User preferred language code (e.g., en, ja). NULL means "not yet set by client"; client backfills via UpdatePreferredLanguage on first observation.';
