-- +goose Up
-- Create Merkle tree nodes table for ZKP identity set per event
CREATE TABLE merkle_tree (
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    depth INT NOT NULL,
    node_index INT NOT NULL,
    hash BYTEA NOT NULL,
    PRIMARY KEY (event_id, depth, node_index)
);

COMMENT ON TABLE merkle_tree IS 'Merkle tree nodes for ZKP identity set per event; canonical tree maintained by backend';
COMMENT ON COLUMN merkle_tree.depth IS 'Tree depth level (0 = leaves, max = root)';
COMMENT ON COLUMN merkle_tree.node_index IS 'Node position at the given depth level';
COMMENT ON COLUMN merkle_tree.hash IS 'Poseidon hash value of the node';

-- Create nullifiers table for double-entry prevention
CREATE TABLE nullifiers (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    nullifier_hash BYTEA NOT NULL,
    used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Prevent double entry: one nullifier per event
CREATE UNIQUE INDEX idx_nullifiers_event_hash ON nullifiers(event_id, nullifier_hash);

COMMENT ON TABLE nullifiers IS 'Used ZKP nullifier hashes for preventing double entry at events';
COMMENT ON COLUMN nullifiers.nullifier_hash IS 'The nullifier hash from the ZK proof; unique per event to prevent reuse';

-- +goose Down
-- Drop nullifiers and merkle_tree tables (indexes dropped automatically with tables)
-- Drop nullifiers first since it has no dependents; merkle_tree has none either
DROP TABLE IF EXISTS nullifiers;
DROP TABLE IF EXISTS merkle_tree;
