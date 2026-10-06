-- Drop two unused leftovers.
-- _series_consolidation_backup: the one-time rollback snapshot taken by
-- 20260826000000_consolidate_fragmented_series, droppable once the
-- consolidation was verified in prod. No code reads it.
-- uuid-ossp: unused since ids became application-generated UUIDv7
-- (20260311171822_enforce_uuidv7_and_cleanup). No CASCADE, so a dependent
-- object fails the migration instead of being dropped with it.
-- Part of upgrade-atlas-operator task 2.1.

SET search_path TO app, public;

DROP TABLE IF EXISTS "_series_consolidation_backup";
DROP EXTENSION IF EXISTS "uuid-ossp";
