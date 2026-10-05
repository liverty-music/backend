-- Add official_site_checked_at column to artists: records when the artist's
-- official site was last checked in MusicBrainz, whatever the check found.
-- NULL means never checked; the official-site-refresh job takes those first.
-- Part of refresh-artist-official-site task 1.1.

SET search_path TO app, public;

ALTER TABLE artists ADD COLUMN IF NOT EXISTS official_site_checked_at TIMESTAMPTZ;

COMMENT ON COLUMN artists.official_site_checked_at IS 'Timestamp of the last official-site check in MusicBrainz for this artist, whatever the check found. NULL when never checked.';
