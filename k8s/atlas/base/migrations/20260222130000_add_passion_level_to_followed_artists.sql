-- Add passion_level column to followed_artists table.
-- Represents the user's enthusiasm tier for a followed artist:
--   'must_go'      — will travel anywhere for concerts
--   'local_only'   — interested in local events only (default)
--   'keep_an_eye'  — display on dashboard but no push notifications
ALTER TABLE followed_artists
    ADD COLUMN passion_level TEXT NOT NULL DEFAULT 'local_only';

ALTER TABLE followed_artists
    ADD CONSTRAINT chk_followed_artists_passion_level
    CHECK (passion_level IN ('must_go', 'local_only', 'keep_an_eye'));

COMMENT ON COLUMN followed_artists.passion_level IS 'User enthusiasm tier: must_go, local_only (default), or keep_an_eye';
