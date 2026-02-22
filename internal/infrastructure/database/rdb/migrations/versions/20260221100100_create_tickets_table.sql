-- +goose Up
-- Create tickets table for Soulbound Ticket (ERC-5192) ownership records
CREATE TABLE tickets (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_id BIGINT NOT NULL,
    tx_hash TEXT NOT NULL,
    minted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Ensure one ticket per user per event
CREATE UNIQUE INDEX idx_tickets_event_user ON tickets(event_id, user_id);
-- Fast lookup by user
CREATE INDEX idx_tickets_user_id ON tickets(user_id);
-- Fast lookup by token_id (for on-chain verification)
CREATE UNIQUE INDEX idx_tickets_token_id ON tickets(token_id);

COMMENT ON TABLE tickets IS 'Soulbound Ticket (ERC-5192) ownership records linking users to event tokens on-chain';
COMMENT ON COLUMN tickets.token_id IS 'On-chain ERC-721 token ID minted on Base Sepolia';
COMMENT ON COLUMN tickets.tx_hash IS 'Blockchain transaction hash of the mint operation';

-- +goose Down
-- Drop tickets table and all associated indexes (indexes dropped automatically with table)
DROP TABLE IF EXISTS tickets;
