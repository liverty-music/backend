-- Rename hype tier value 'anywhere' to 'away'.

-- 1. Update existing rows.
UPDATE followed_artists SET hype = 'away' WHERE hype = 'anywhere';

-- 2. Change the default value.
ALTER TABLE followed_artists ALTER COLUMN hype SET DEFAULT 'away';

-- 3. Replace the CHECK constraint.
ALTER TABLE followed_artists DROP CONSTRAINT chk_followed_artists_hype;
ALTER TABLE followed_artists ADD CONSTRAINT chk_followed_artists_hype
    CHECK (hype IN ('watch', 'home', 'nearby', 'away'));

-- 4. Update column comment.
COMMENT ON COLUMN followed_artists.hype IS 'User enthusiasm tier: watch (no notifications), home (home area only), nearby (reserved), or away (all concerts, default)';
