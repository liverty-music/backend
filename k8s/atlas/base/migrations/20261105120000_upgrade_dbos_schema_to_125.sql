-- atlas:txmode none

-- DBOS Transact system migrations 124 and 125, required by
-- github.com/dbos-inc/dbos-transact-golang v1.8.0. The workloads start DBOS
-- with SkipMigrations and refuse to start while the schema is behind, so this
-- migration must reach a database before the upgraded workloads do
-- (backend/AGENTS.md, "Durable workflows").
--
-- No transaction: CREATE INDEX CONCURRENTLY is refused inside a transaction
-- block. Both statements only add indexes to dbos.application_versions; no new
-- table, sequence or function, so the grants and default privileges of
-- 20261027120000_create_dbos_schema.sql need no addition.
--
-- The block between the marker lines is the output of
-- `dbos migrate --print-migrations 124` of v1.8.0, unchanged, without its two
-- header comment lines. TestDBOSMigrationsMatchModule compares it with the
-- module.

-- BEGIN dbos migrate --print-migrations 124
-- Migration 124
-- Migration 124: Index application version history for latest-version lookups.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_application_versions_timestamp" ON "dbos"."application_versions" ("version_timestamp" DESC);
UPDATE "dbos".dbos_migrations SET version = 124;
-- Migration 125
-- Migration 125: Index application version history for latest-version lookups.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_application_versions_owner_timestamp" ON "dbos"."application_versions" ("application_name", "version_timestamp" DESC);
UPDATE "dbos".dbos_migrations SET version = 125;
-- END dbos migrate --print-migrations 124
