-- Add CHECK constraints for ticket system data integrity.

ALTER TABLE users
  ADD CONSTRAINT chk_safe_address_format
  CHECK (safe_address IS NULL OR safe_address ~ '^0x[0-9a-fA-F]{40}$');

ALTER TABLE merkle_tree
  ADD CONSTRAINT chk_merkle_depth_positive
  CHECK (depth >= 0);

ALTER TABLE merkle_tree
  ADD CONSTRAINT chk_merkle_index_positive
  CHECK (node_index >= 0);

ALTER TABLE merkle_tree
  ADD CONSTRAINT chk_merkle_hash_size
  CHECK (octet_length(hash) = 32);

ALTER TABLE nullifiers
  ADD CONSTRAINT chk_nullifier_hash_size
  CHECK (octet_length(nullifier_hash) = 32);
