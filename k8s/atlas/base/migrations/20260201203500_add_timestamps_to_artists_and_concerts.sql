-- Add created_at and updated_at to artists and concerts tables
ALTER TABLE artists ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE artists ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE concerts ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE concerts ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

COMMENT ON COLUMN artists.created_at IS 'Timestamp when the artist was created';
COMMENT ON COLUMN artists.updated_at IS 'Timestamp when the artist was last updated';
COMMENT ON COLUMN concerts.created_at IS 'Timestamp when the concert was created';
COMMENT ON COLUMN concerts.updated_at IS 'Timestamp when the concert was last updated';
