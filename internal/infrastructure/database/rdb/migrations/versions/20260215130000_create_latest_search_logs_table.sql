-- +goose Up
-- Create latest_search_logs table for tracking when each artist was last searched
-- via external sources (e.g., Gemini API). Used to skip redundant API calls.
CREATE TABLE IF NOT EXISTS latest_search_logs (
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    searched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (artist_id)
);

COMMENT ON TABLE latest_search_logs IS 'Tracks when each artist was last searched for concerts via external APIs';
COMMENT ON COLUMN latest_search_logs.artist_id IS 'Reference to the artist that was searched';
COMMENT ON COLUMN latest_search_logs.searched_at IS 'Timestamp of the most recent external search';
