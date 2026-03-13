-- Modify "followed_artists" table
ALTER TABLE "followed_artists" ALTER COLUMN "hype" SET DEFAULT 'watch';
-- Set comment to column: "hype" on table: "followed_artists"
COMMENT ON COLUMN "followed_artists"."hype" IS 'User enthusiasm tier: watch (no notifications, default), home (home area only), nearby (reserved), or away (all concerts)';
