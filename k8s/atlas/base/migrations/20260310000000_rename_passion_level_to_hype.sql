-- Rename passion_level column to hype on followed_artists table.
-- Migrates from 3-tier PassionLevel (must_go, local_only, keep_an_eye) to
-- 4-tier Hype system (watch, home, nearby, anywhere).
--
-- Value mapping:
--   must_go     → anywhere  (same intent: travel anywhere)
--   local_only  → anywhere  (bump to new default; notifications were never filtered)
--   keep_an_eye → watch     (same intent: no notifications)

-- Step 1: Drop the old CHECK constraint
ALTER TABLE followed_artists
    DROP CONSTRAINT chk_followed_artists_passion_level;

-- Step 2: Rename the column
ALTER TABLE followed_artists
    RENAME COLUMN passion_level TO hype;

-- Step 3: Map existing values to new tier names
UPDATE followed_artists SET hype = 'anywhere' WHERE hype IN ('must_go', 'local_only');
UPDATE followed_artists SET hype = 'watch' WHERE hype = 'keep_an_eye';

-- Step 4: Set the new default
ALTER TABLE followed_artists
    ALTER COLUMN hype SET DEFAULT 'anywhere';

-- Step 5: Add the new CHECK constraint
ALTER TABLE followed_artists
    ADD CONSTRAINT chk_followed_artists_hype
    CHECK (hype IN ('watch', 'home', 'nearby', 'anywhere'));

COMMENT ON COLUMN followed_artists.hype IS 'User enthusiasm tier: watch (no notifications), home (home area only), nearby (reserved), or anywhere (all concerts, default)';
