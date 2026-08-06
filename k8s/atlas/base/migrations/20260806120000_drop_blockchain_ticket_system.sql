-- Drop blockchain/ZKP ticket system tables and columns.
-- Dependent tables are dropped first to satisfy foreign-key constraints.

-- nullifiers references events(id) via event_id
DROP TABLE IF EXISTS nullifiers;

-- merkle_tree references events(id) via event_id
DROP TABLE IF EXISTS merkle_tree;

-- tickets references events(id) and users(id)
DROP TABLE IF EXISTS tickets;

-- Remove Safe (ERC-4337) address column added for blockchain identity
ALTER TABLE users DROP COLUMN IF EXISTS safe_address;

-- Remove Merkle root column added for ZKP identity set
ALTER TABLE events DROP COLUMN IF EXISTS merkle_root;
