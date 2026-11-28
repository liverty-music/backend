-- Modify "artists" table
ALTER TABLE "artists" ADD COLUMN "other_mbids" text[] NOT NULL DEFAULT '{}';
-- Set comment to column: "other_mbids" on table: "artists"
COMMENT ON COLUMN "artists"."other_mbids" IS 'MBIDs the artist was corrected from: the music catalog''s lookup by name returned a namesake''s MBID, and a search that returns one of them still finds this artist instead of registering a second row. Never the artist''s own mbid; an MBID is the mbid or other MBID of one artist only (checked by the application under an advisory lock).';
