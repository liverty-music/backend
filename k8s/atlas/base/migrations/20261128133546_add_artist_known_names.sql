-- Modify "artists" table
ALTER TABLE "artists" ADD COLUMN "known_names" text[] NOT NULL DEFAULT '{}';
-- Backfill: until the daily refresh has read its aliases, an artist is known by its name, normalized as the
-- application normalizes it (Unicode NFKC, lowercased, surrounding white space removed, inner runs made one space)
UPDATE "artists" SET "known_names" = ARRAY[lower(btrim(regexp_replace(normalize("name", NFKC), '\s+', ' ', 'g')))];
-- Create index "idx_artists_known_names" to table: "artists"
CREATE INDEX "idx_artists_known_names" ON "artists" USING GIN ("known_names");
-- Set comment to column: "known_names" on table: "artists"
COMMENT ON COLUMN "artists"."known_names" IS 'Names the artist is found by: its name and every alias MusicBrainz gives for it, each once and normalized (Unicode NFKC, lowercased, surrounding white space removed, inner runs made one space). A similar artist the listening service lists without an MBID is matched against them.';
