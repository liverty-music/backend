-- fan-passkey-recovery-and-management (design D8): User.GetByEmail matches
-- the address with letter case ignored (lower(email) = lower($1)); this index
-- serves that lookup. The existing UNIQUE (email) stays case-sensitive; a
-- read-only prod check before this migration found no two addresses that
-- differ only in letter case.

SET search_path TO app, public;

-- Create index "idx_users_email_lower" to table: "users"
CREATE INDEX IF NOT EXISTS "idx_users_email_lower" ON "users" ((lower(email)));
-- Set comment to index: "idx_users_email_lower" on table: "users"
COMMENT ON INDEX "idx_users_email_lower" IS 'Speeds up the user lookup by email that ignores letter case (passkey recovery)';
