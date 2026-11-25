-- atlas:txmode none

-- DBOS Transact system migrations 126 to 130, required by
-- github.com/dbos-inc/dbos-transact-golang v1.9.0. The workloads start DBOS
-- with SkipMigrations and refuse to start while the schema is behind, so this
-- migration must reach a database before the upgraded workloads do
-- (backend/AGENTS.md, "Durable workflows").
--
-- No transaction: CREATE/DROP INDEX CONCURRENTLY is refused inside a
-- transaction block. The migrations add one column and two indexes to
-- dbos.workflow_status, drop one superseded index, and replace the function
-- dbos.send_message (CREATE OR REPLACE, then pin its search_path). No new
-- table, sequence or function, so the grants and default privileges of
-- 20261027120000_create_dbos_schema.sql need no addition.
--
-- The block between the marker lines is the output of
-- `dbos migrate --print-migrations 126` of v1.9.0, unchanged, without its two
-- header comment lines. TestDBOSMigrationsMatchModule compares it with the
-- module.

-- BEGIN dbos migrate --print-migrations 126
-- Migration 126
-- Migration 126: Topic a SUSPENDED recv waits on.

ALTER TABLE "dbos"."workflow_status"
    ADD COLUMN IF NOT EXISTS "suspend_key" TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 126;
-- Migration 127
-- Migration 127: Partial index for the sweep that re-enqueues SUSPENDED workflows.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_suspended" ON "dbos"."workflow_status" ("delay_until_epoch_ms") WHERE "status" = 'SUSPENDED';
UPDATE "dbos".dbos_migrations SET version = 127;
-- Migration 128
-- Migration 128: idx_workflow_status_deadline (migration 122) with a predicate
-- that also covers SUSPENDED workflows. Supersedes it; dropped by migration 129.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_deadline_v2" ON "dbos"."workflow_status" ("workflow_deadline_epoch_ms") WHERE "status" IN ('ENQUEUED', 'PENDING', 'DELAYED', 'SUSPENDED') AND "workflow_deadline_epoch_ms" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 128;
-- Migration 129
-- Migration 129: Drop idx_workflow_status_deadline (migration 122),
-- superseded by idx_workflow_status_deadline_v2 (migration 128).

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."idx_workflow_status_deadline";
UPDATE "dbos".dbos_migrations SET version = 129;
-- Migration 130
-- Migration 130: send_message also wakes a SUSPENDED recv on its topic.

CREATE OR REPLACE FUNCTION "dbos".send_message(
    destination_id TEXT,
    message JSON,
    topic TEXT DEFAULT NULL,
    message_id TEXT DEFAULT NULL
) RETURNS VOID AS $$
DECLARE
    v_topic TEXT := COALESCE(topic, '__null__topic__');
    v_message_id TEXT := COALESCE(message_id, gen_random_uuid()::TEXT);
BEGIN
    INSERT INTO "dbos".notifications (
        destination_uuid, topic, message, message_uuid, serialization
    ) VALUES (
        destination_id, v_topic, message, v_message_id, 'portable_json'
    )
    ON CONFLICT (message_uuid) DO NOTHING;
    UPDATE "dbos".workflow_status
    -- Status is tested in SET, not WHERE, so the planner cannot scan idx_workflow_status_suspended.
    SET delay_until_epoch_ms = CASE WHEN status = 'SUSPENDED' THEN 0 ELSE delay_until_epoch_ms END
    WHERE workflow_uuid = destination_id
        AND suspend_key = v_topic;
EXCEPTION
    WHEN foreign_key_violation THEN
        RAISE EXCEPTION 'DBOS non-existent workflow'
            USING DETAIL = format('Destination workflow %s does not exist', destination_id),
                ERRCODE = 'foreign_key_violation';
END;
$$ LANGUAGE plpgsql;

-- Migration 130 (Postgres-only tail): re-pin search_path on send_message.

ALTER FUNCTION "dbos".send_message(
    TEXT, JSON, TEXT, TEXT
) SET search_path = pg_catalog, pg_temp;
UPDATE "dbos".dbos_migrations SET version = 130;
-- END dbos migrate --print-migrations 126
