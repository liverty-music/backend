-- hide-ended-artists-from-discovery: whether MusicBrainz marks an artist's
-- activity as ended (design Decision 1). A constant default makes this a
-- metadata-only change; the table is not rewritten.
SET search_path TO app, public;

-- Modify "artists" table
ALTER TABLE "artists" ADD COLUMN "activity_ended" boolean NOT NULL DEFAULT false;
-- Set comment to column: "activity_ended" on table: "artists"
COMMENT ON COLUMN "artists"."activity_ended" IS 'True when MusicBrainz marks the artist''s activity as ended (a group dissolved, a solo artist died); false when it does not or before the artist was first read from MusicBrainz. Charts and similar-artist lists leave out ended artists, and concert and ticket-sale searches skip them.';
