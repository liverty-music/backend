-- +goose Up
-- Fix ticket system schema issues identified in PR review

-- 1. Change token_id from BIGINT to NUMERIC(78, 0) to support the full uint256 range
--    used by Solidity. BIGINT (max ~9.2e18) cannot hold values beyond 2^63-1,
--    which is a subset of the ERC-721 uint256 token ID space.
ALTER TABLE tickets ALTER COLUMN token_id TYPE NUMERIC(78, 0);

-- 2. Add UNIQUE constraint on users.safe_address to enforce the 1:1 mapping
--    between users and their deterministically predicted Safe (ERC-4337) addresses.
ALTER TABLE users ADD CONSTRAINT users_safe_address_unique UNIQUE (safe_address);

-- +goose Down
-- Revert fix_ticket_system_schema changes
-- Remove the UNIQUE constraint added to users.safe_address
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_safe_address_unique;
-- Revert token_id back to BIGINT (note: values exceeding BIGINT range would cause this to fail)
ALTER TABLE tickets ALTER COLUMN token_id TYPE BIGINT USING token_id::BIGINT;
