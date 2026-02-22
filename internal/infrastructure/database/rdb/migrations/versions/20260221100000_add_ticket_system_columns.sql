-- +goose Up
-- Add ticket system columns to existing tables

-- Add Safe address column to users (predicted ERC-4337 Smart Account address)
ALTER TABLE users ADD COLUMN safe_address TEXT;
COMMENT ON COLUMN users.safe_address IS 'Predicted Safe (ERC-4337) address derived deterministically from users.id via CREATE2';

-- Add merkle_root to events (for ZKP identity set; NULL means non-ticketed event)
ALTER TABLE events ADD COLUMN merkle_root BYTEA;
COMMENT ON COLUMN events.merkle_root IS 'Merkle tree root hash for ZKP identity set; NULL for non-ticket events';

-- +goose Down
-- Remove ticket system columns from existing tables (backward-compatible: columns were nullable)
ALTER TABLE events DROP COLUMN IF EXISTS merkle_root;
ALTER TABLE users DROP COLUMN IF EXISTS safe_address;
