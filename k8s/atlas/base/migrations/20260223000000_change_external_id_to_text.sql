-- Change users.external_id from UUID to TEXT to accept Zitadel snowflake IDs.
-- Zitadel uses numeric string IDs (e.g., "360952429480515994"), not UUIDs.
-- Existing UUID values are valid TEXT, so this is a data-compatible change.

ALTER TABLE users ALTER COLUMN external_id TYPE TEXT;
