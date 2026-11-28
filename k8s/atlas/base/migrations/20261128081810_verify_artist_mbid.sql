-- Modify "artists" table
ALTER TABLE "artists" ADD COLUMN "listening_mbid" text, ADD COLUMN "mbid_status" text NOT NULL DEFAULT 'unverified';
-- Backfill: until an MBID is corrected the listening service files the artist under its own MBID
UPDATE "artists" SET "listening_mbid" = "mbid";
-- The existing test artists are Verified: an admin registered them with a generated MBID the catalog does not know,
-- which VerifyMBID would find Ambiguous and CorrectMBID could not fix. On 2026-11-28 MusicBrainz answered 404 for one
-- artist's MBID among the 5,505 in prod, "Liverty Music Band" of the Organizer e2e-test, which the owner confirmed is a
-- test artist.
UPDATE "artists" SET "mbid_status" = 'verified' WHERE "id" = '01a16d79-1fe5-7825-9fb0-dd6818f202f0';
-- Penthouse's mbid was corrected by hand to 9604bb00-65c5-4b67-a174-c46c063ba794 (read from prod on 2026-11-28);
-- the listening service still returns the namesake's MBID c94eaf81-baae-465d-8197-9951c2ef52cd for "Penthouse",
-- which no artist held as its mbid on 2026-11-28 (read-only prod check)
UPDATE "artists" SET "listening_mbid" = 'c94eaf81-baae-465d-8197-9951c2ef52cd' WHERE "mbid" = '9604bb00-65c5-4b67-a174-c46c063ba794';
ALTER TABLE "artists" ALTER COLUMN "listening_mbid" SET NOT NULL, ADD CONSTRAINT "chk_artists_listening_mbid_format" CHECK (char_length(listening_mbid) = 36), ADD CONSTRAINT "chk_artists_mbid_status" CHECK (mbid_status = ANY (ARRAY['unverified'::text, 'verified'::text, 'ambiguous'::text])), DROP COLUMN "other_mbids";
-- Create index "idx_artists_listening_mbid" to table: "artists"
CREATE UNIQUE INDEX "idx_artists_listening_mbid" ON "artists" ("listening_mbid");
-- Set comment to column: "mbid" on table: "artists"
COMMENT ON COLUMN "artists"."mbid" IS 'The artist''s own MusicBrainz Identifier (MBID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx); differs from listening_mbid when the listening service files the artist under a namesake''s MBID';
-- Set comment to column: "activity_ended" on table: "artists"
COMMENT ON COLUMN "artists"."activity_ended" IS 'True when MusicBrainz marks the artist''s activity as ended (a group dissolved, a solo artist died); false when it does not or before the artist was first read from MusicBrainz. Name search, charts and similar-artist lists leave out ended artists, and concert and ticket-sale searches skip them.';
-- Set comment to column: "listening_mbid" on table: "artists"
COMMENT ON COLUMN "artists"."listening_mbid" IS 'The MBID the listening service (Last.fm) files the artist under: name search, charts and similar artists return it, and similar artists are looked up by it. Equal to mbid until the mbid is corrected; it never changes. An MBID is the mbid or listening_mbid of at most one artist (checked by the application under an advisory lock).';
-- Set comment to column: "mbid_status" on table: "artists"
COMMENT ON COLUMN "artists"."mbid_status" IS 'Whether mbid was checked against the music catalog''s artists of the same name: unverified (not yet), verified (checked, automatically or by an admin), ambiguous (an admin must choose). Only verified artists whose activity has not ended are offered to fans.';
