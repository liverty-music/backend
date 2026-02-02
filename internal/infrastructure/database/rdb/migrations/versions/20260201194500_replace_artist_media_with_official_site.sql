-- Drop artist_media table and its index
DROP INDEX IF EXISTS idx_artist_media_artist_id;
DROP TABLE IF EXISTS artist_media;

-- Create artist_official_site table
CREATE TABLE IF NOT EXISTS artist_official_site (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE UNIQUE,
    url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Add comments for artist_official_site
COMMENT ON TABLE artist_official_site IS 'Stores the official website URL for each artist, used for concert search grounding.';
COMMENT ON COLUMN artist_official_site.id IS 'Unique identifier (UUIDv7)';
COMMENT ON COLUMN artist_official_site.artist_id IS 'Reference to the artist (1:1 relationship)';
COMMENT ON COLUMN artist_official_site.url IS 'Official artist website URL';
COMMENT ON COLUMN artist_official_site.created_at IS 'Timestamp when the record was created';
COMMENT ON COLUMN artist_official_site.updated_at IS 'Timestamp when the record was last updated';

-- Add index for artist_id
CREATE INDEX IF NOT EXISTS idx_artist_official_site_artist_id ON artist_official_site(artist_id);
COMMENT ON INDEX idx_artist_official_site_artist_id IS 'Optimizes retrieval of official site for an artist';
