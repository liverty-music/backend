-- Add source_url to concerts table
ALTER TABLE concerts ADD COLUMN IF NOT EXISTS source_url TEXT;

COMMENT ON COLUMN concerts.source_url IS 'Official source URL or informational link for the concert';
