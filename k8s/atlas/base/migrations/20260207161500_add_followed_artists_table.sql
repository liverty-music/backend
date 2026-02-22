-- Drop the old user_artist_subscriptions table if it exists
DROP TABLE IF EXISTS user_artist_subscriptions;

-- Create followed_artists table for tracking user follows
-- This table uses a composite primary key as it is a pure junction table.
CREATE TABLE IF NOT EXISTS followed_artists (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, artist_id)
);

COMMENT ON TABLE followed_artists IS 'Tracks which artists a user is following for discovery and personalization';
COMMENT ON COLUMN followed_artists.user_id IS 'Reference to the user who is following';
COMMENT ON COLUMN followed_artists.artist_id IS 'Reference to the artist being followed';
COMMENT ON COLUMN followed_artists.created_at IS 'Timestamp when the follow occurred';

-- Index for listing followed artists by user
CREATE INDEX IF NOT EXISTS idx_followed_artists_user_id ON followed_artists(user_id);
-- Index for counting/listing followers of an artist
CREATE INDEX IF NOT EXISTS idx_followed_artists_artist_id ON followed_artists(artist_id);
