-- +goose Up
-- Add external_id column to users table for Zitadel identity mapping
ALTER TABLE users ADD COLUMN external_id UUID UNIQUE NOT NULL;

COMMENT ON COLUMN users.external_id IS 'Zitadel identity provider user ID (sub claim), used for account sync';

-- Index for fast lookup by external_id
CREATE INDEX IF NOT EXISTS idx_users_external_id ON users(external_id);
