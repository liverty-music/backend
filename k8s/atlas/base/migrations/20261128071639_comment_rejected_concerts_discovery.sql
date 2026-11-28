-- Set comment to table: "rejected_concerts"
COMMENT ON TABLE "rejected_concerts" IS 'Append-only record of rejected concert candidates, used for searcher-quality analysis and by discovery, which leaves out a found event that is the same listing (artist, local date, listed venue, start time) as an entry.';
-- Set comment to index: "idx_rejected_concerts_artist_id" on table: "rejected_concerts"
COMMENT ON INDEX "idx_rejected_concerts_artist_id" IS 'Supports per-artist analysis of repeated rejection patterns and discovery reading an artist''s entries';
