-- fix-backend-contract-drift: one User per email address when letter case is
-- ignored. The column's UNIQUE compared letter case, so "Fan@Example.com" and
-- "fan@example.com" could both be stored, after which the lookup that ignores
-- case (passkey recovery) found two users. The unique index on lower(email)
-- replaces the constraint and both plain indexes: no query matches the email
-- exactly, and the unique index serves the lookup that ignores case.
-- Production was checked read-only before this migration: no two rows share an
-- address in different letter case. The table is small, so the index is built
-- without CONCURRENTLY.
SET search_path TO app, public;

-- Drop index "idx_users_email" from table: "users"
DROP INDEX "idx_users_email";
-- Drop index "idx_users_email_lower" from table: "users"
DROP INDEX "idx_users_email_lower";
-- Modify "users" table
ALTER TABLE "users" DROP CONSTRAINT "users_email_key";
-- Create index "uq_users_email_lower" to table: "users"
CREATE UNIQUE INDEX "uq_users_email_lower" ON "users" ((lower(email)));
-- Set comment to index: "uq_users_email_lower" on table: "users"
COMMENT ON INDEX "uq_users_email_lower" IS 'One user per email address when letter case is ignored; also serves the lookup by email that ignores letter case (passkey recovery)';
