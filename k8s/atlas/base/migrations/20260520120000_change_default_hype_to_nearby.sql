-- Modify "followed_artists" table
ALTER TABLE "followed_artists" ALTER COLUMN "hype" SET DEFAULT 'nearby';
-- Set comment to column: "hype" on table: "followed_artists"
COMMENT ON COLUMN "followed_artists"."hype" IS 'User enthusiasm tier: watch (no notifications), home (home area only), nearby (within ~200km of home, default), or away (all concerts)';
