-- atlas:txmode none

-- The DBOS Transact system tables in schema `dbos` (OpenSpec change
-- introduce-durable-workflows, design "Placement, migrations and connections").
-- The workloads start DBOS with SkipMigrations, so this migration is the only
-- place the tables are created; at start DBOS verifies the schema version and
-- fails if it is behind.
--
-- No transaction: the DBOS SQL builds indexes with CREATE INDEX CONCURRENTLY,
-- which Postgres refuses inside a transaction block.
--
-- The block between the two marker lines is the output of
-- `dbos migrate --print-migrations all` of the module version pinned in go.mod,
-- unchanged. TestDBOSMigrationsMatchModule compares it with the module, so
-- edit it only by pasting that output again. To upgrade the module, add a new
-- migration with the output of `--print-migrations <first new number>` between
-- the same markers (backend/AGENTS.md, "Durable workflows").

-- BEGIN dbos migrate --print-migrations 1
CREATE SCHEMA IF NOT EXISTS "dbos";
CREATE TABLE IF NOT EXISTS "dbos".dbos_migrations (version BIGINT NOT NULL PRIMARY KEY);
-- Migration 1
CREATE TABLE "dbos".workflow_status (
    workflow_uuid TEXT PRIMARY KEY,
    status TEXT,
    name TEXT,
    authenticated_user TEXT,
    assumed_role TEXT,
    authenticated_roles TEXT,
    request TEXT,
    output TEXT,
    error TEXT,
    executor_id TEXT,
    created_at BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now())::numeric * 1000)::bigint,
    updated_at BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now())::numeric * 1000)::bigint,
    application_version TEXT,
    application_id TEXT,
    class_name VARCHAR(255) DEFAULT NULL,
    config_name VARCHAR(255) DEFAULT NULL,
    recovery_attempts BIGINT DEFAULT 0,
    queue_name TEXT,
    workflow_timeout_ms BIGINT,
    workflow_deadline_epoch_ms BIGINT,
    inputs TEXT,
    started_at_epoch_ms BIGINT,
    deduplication_id TEXT,
    priority INT4 NOT NULL DEFAULT 0
);

CREATE INDEX workflow_status_created_at_index ON "dbos".workflow_status (created_at);
CREATE INDEX workflow_status_executor_id_index ON "dbos".workflow_status (executor_id);
CREATE INDEX workflow_status_status_index ON "dbos".workflow_status (status);

ALTER TABLE "dbos".workflow_status
ADD CONSTRAINT uq_workflow_status_queue_name_dedup_id
UNIQUE (queue_name, deduplication_id);

CREATE TABLE "dbos".operation_outputs (
    workflow_uuid TEXT NOT NULL,
    function_id INT4 NOT NULL,
    function_name TEXT NOT NULL DEFAULT '',
    output TEXT,
    error TEXT,
    child_workflow_id TEXT,
    PRIMARY KEY (workflow_uuid, function_id),
    FOREIGN KEY (workflow_uuid) REFERENCES "dbos".workflow_status(workflow_uuid) 
        ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE "dbos".notifications (
    destination_uuid TEXT NOT NULL,
    topic TEXT,
    message TEXT NOT NULL,
    created_at_epoch_ms BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now())::numeric * 1000)::bigint,
    message_uuid TEXT NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY, -- Built-in function
    FOREIGN KEY (destination_uuid) REFERENCES "dbos".workflow_status(workflow_uuid) 
        ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE INDEX idx_workflow_topic ON "dbos".notifications (destination_uuid, topic);

CREATE TABLE "dbos".workflow_events (
    workflow_uuid TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (workflow_uuid, key),
    FOREIGN KEY (workflow_uuid) REFERENCES "dbos".workflow_status(workflow_uuid) 
        ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE "dbos".streams (
    workflow_uuid TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    "offset" INT4 NOT NULL,
    PRIMARY KEY (workflow_uuid, key, "offset"),
    FOREIGN KEY (workflow_uuid) REFERENCES "dbos".workflow_status(workflow_uuid)
        ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE "dbos".event_dispatch_kv (
    service_name TEXT NOT NULL,
    workflow_fn_name TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    update_seq NUMERIC(38,0),
    update_time NUMERIC(38,15),
    PRIMARY KEY (service_name, workflow_fn_name, key)
);
-- Create notification function
CREATE OR REPLACE FUNCTION "dbos".notifications_function() RETURNS TRIGGER AS $$
DECLARE
    payload text := NEW.destination_uuid || '::' || NEW.topic;
BEGIN
    PERFORM pg_notify('dbos_notifications_channel', payload);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create notification trigger
CREATE TRIGGER dbos_notifications_trigger
AFTER INSERT ON "dbos".notifications
FOR EACH ROW EXECUTE FUNCTION "dbos".notifications_function();

-- Create events function
CREATE OR REPLACE FUNCTION "dbos".workflow_events_function() RETURNS TRIGGER AS $$
DECLARE
    payload text := NEW.workflow_uuid || '::' || NEW.key;
BEGIN
    PERFORM pg_notify('dbos_workflow_events_channel', payload);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create events trigger
CREATE TRIGGER dbos_workflow_events_trigger
AFTER INSERT ON "dbos".workflow_events
FOR EACH ROW EXECUTE FUNCTION "dbos".workflow_events_function();
INSERT INTO "dbos".dbos_migrations (version) VALUES (1);
-- Migration 2
-- Migration 2: Add queue_partition_key column to workflow_status table
-- This enables partitioned queues where workflows can be distributed across
-- dynamically created queue partitions with separate concurrency limits per partition.

ALTER TABLE "dbos".workflow_status
ADD COLUMN queue_partition_key TEXT;
UPDATE "dbos".dbos_migrations SET version = 2;
-- Migration 3
-- Migration 3: Add index on workflow_status for queue, status, and started_at_epoch_ms
-- This index improves query performance for queue operations that filter by queue_name, status, and started_at_epoch_ms

CREATE INDEX "idx_workflow_status_queue_status_started" ON "dbos"."workflow_status" ("queue_name", "status", "started_at_epoch_ms");
UPDATE "dbos".dbos_migrations SET version = 3;
-- Migration 4
-- Migration 4: Add forked_from column to workflow_status table
-- This enables tracking workflow fork lineage

ALTER TABLE "dbos".workflow_status
ADD COLUMN forked_from TEXT;

CREATE INDEX "idx_workflow_status_forked_from" ON "dbos"."workflow_status" ("forked_from");
UPDATE "dbos".dbos_migrations SET version = 4;
-- Migration 5
-- Migration 5: Add started_at_epoch_ms and completed_at_epoch_ms columns to operation_outputs table
-- This enables visualization of step duration

ALTER TABLE "dbos".operation_outputs
ADD COLUMN started_at_epoch_ms BIGINT, ADD COLUMN completed_at_epoch_ms BIGINT;
UPDATE "dbos".dbos_migrations SET version = 5;
-- Migration 6
-- Migration 6: Add workflow_events_history table and function_id to streams table
-- This enables tracking event history by step ID and copying events during workflow forking

CREATE TABLE "dbos".workflow_events_history (
    workflow_uuid TEXT NOT NULL,
    function_id INT4 NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (workflow_uuid, function_id, key),
    FOREIGN KEY (workflow_uuid) REFERENCES "dbos".workflow_status(workflow_uuid) 
        ON UPDATE CASCADE ON DELETE CASCADE
);

ALTER TABLE "dbos".streams ADD COLUMN function_id INT4 NOT NULL DEFAULT 0;
UPDATE "dbos".dbos_migrations SET version = 6;
-- Migration 7
-- Migration 7: Add owner_xid column to workflow_status table

ALTER TABLE "dbos".workflow_status ADD COLUMN owner_xid TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 7;
-- Migration 8
-- Migration 8: Add parent_workflow_id column to workflow_status table
-- This enables tracking child workflow lineage for workflows spawned from within other workflows.

ALTER TABLE "dbos".workflow_status
ADD COLUMN parent_workflow_id TEXT DEFAULT NULL;

CREATE INDEX "idx_workflow_status_parent_workflow_id" ON "dbos"."workflow_status" ("parent_workflow_id");
UPDATE "dbos".dbos_migrations SET version = 8;
-- Migration 9
-- Migration 9: Add workflow_schedules table
-- Stores schedule metadata for workflow cron/interval scheduling.

CREATE TABLE "dbos".workflow_schedules (
    schedule_id TEXT PRIMARY KEY,
    schedule_name TEXT NOT NULL UNIQUE,
    workflow_name TEXT NOT NULL,
    workflow_class_name TEXT,
    schedule TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    context TEXT NOT NULL
);
UPDATE "dbos".dbos_migrations SET version = 9;
-- Migration 10 skipped: not applicable on fresh databases
UPDATE "dbos".dbos_migrations SET version = 10;
-- Migration 11
-- Migration 11: Add serialization column to workflow and event tables
-- Stores serialization format/metadata for workflow data.

ALTER TABLE "dbos".workflow_status ADD COLUMN serialization TEXT DEFAULT NULL;
ALTER TABLE "dbos".notifications ADD COLUMN serialization TEXT DEFAULT NULL;
ALTER TABLE "dbos".workflow_events ADD COLUMN serialization TEXT DEFAULT NULL;
ALTER TABLE "dbos".workflow_events_history ADD COLUMN serialization TEXT DEFAULT NULL;
ALTER TABLE "dbos".operation_outputs ADD COLUMN serialization TEXT DEFAULT NULL;
ALTER TABLE "dbos".streams ADD COLUMN serialization TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 11;
-- Migration 12
-- Migration 12: Add consumed column to notifications and index for unconsumed lookups.

ALTER TABLE "dbos".notifications ADD COLUMN consumed BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX "idx_notifications" ON "dbos".notifications (destination_uuid, topic);
UPDATE "dbos".dbos_migrations SET version = 12;
-- Migration 13
-- Migration 13: Add application_versions table for version tracking.

CREATE TABLE "dbos".application_versions (
    version_id TEXT NOT NULL PRIMARY KEY,
    version_name TEXT NOT NULL UNIQUE,
    version_timestamp BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now())::numeric * 1000)::bigint,
    created_at BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now())::numeric * 1000)::bigint
);
UPDATE "dbos".dbos_migrations SET version = 13;
-- Migration 14
-- Migration 14: Add plpgsql stored functions for direct SQL client access.

CREATE FUNCTION "dbos".enqueue_workflow(
    workflow_name TEXT,
    queue_name TEXT,
    positional_args JSON[] DEFAULT ARRAY[]::JSON[],
    named_args JSON DEFAULT '{}'::JSON,
    class_name TEXT DEFAULT NULL,
    config_name TEXT DEFAULT NULL,
    workflow_id TEXT DEFAULT NULL,
    app_version TEXT DEFAULT NULL,
    timeout_ms BIGINT DEFAULT NULL,
    deadline_epoch_ms BIGINT DEFAULT NULL,
    deduplication_id TEXT DEFAULT NULL,
    priority INT4 DEFAULT NULL,
    queue_partition_key TEXT DEFAULT NULL
) RETURNS TEXT AS $$
DECLARE
    v_workflow_id TEXT;
    v_serialized_inputs TEXT;
    v_owner_xid TEXT;
    v_now BIGINT;
    v_recovery_attempts INT4 := 0;
    v_priority INT4;
BEGIN

    -- Validate required parameters
    IF workflow_name IS NULL OR workflow_name = '' THEN
        RAISE EXCEPTION 'Workflow name cannot be null or empty';
    END IF;
    IF queue_name IS NULL OR queue_name = '' THEN
        RAISE EXCEPTION 'Queue name cannot be null or empty';
    END IF;
    IF named_args IS NOT NULL AND jsonb_typeof(named_args::jsonb) != 'object' THEN
        RAISE EXCEPTION 'Named args must be a JSON object';
    END IF;
    IF workflow_id IS NOT NULL AND workflow_id = '' THEN
        RAISE EXCEPTION 'Workflow ID cannot be an empty string if provided.';
    END IF;

    v_workflow_id := COALESCE(workflow_id, gen_random_uuid()::TEXT);
    v_owner_xid := gen_random_uuid()::TEXT;
    v_priority := COALESCE(priority, 0);
    v_serialized_inputs := json_build_object(
        'positionalArgs', positional_args,
        'namedArgs', named_args
    )::TEXT;
    v_now := EXTRACT(epoch FROM now()) * 1000;

    INSERT INTO "dbos".workflow_status (
        workflow_uuid, status, inputs,
        name, class_name, config_name,
        authenticated_user, assumed_role,
        queue_name, deduplication_id, priority, queue_partition_key,
        application_version,
        created_at, updated_at, recovery_attempts,
        workflow_timeout_ms, workflow_deadline_epoch_ms,
        parent_workflow_id, owner_xid, serialization
    ) VALUES (
        v_workflow_id, 'ENQUEUED', v_serialized_inputs,
        workflow_name, class_name, config_name,
        '', '',
        queue_name, deduplication_id, v_priority, queue_partition_key,
        app_version,
        v_now, v_now, v_recovery_attempts,
        timeout_ms, deadline_epoch_ms,
        NULL, v_owner_xid, 'portable_json'
    )
    ON CONFLICT (workflow_uuid)
    DO UPDATE SET
        updated_at = EXCLUDED.updated_at;

    RETURN v_workflow_id;

EXCEPTION
    WHEN unique_violation THEN
        RAISE EXCEPTION 'DBOS queue duplicated'
            USING DETAIL = format('Workflow %s with queue %s and deduplication ID %s already exists', v_workflow_id, queue_name, deduplication_id),
                ERRCODE = 'unique_violation';
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION "dbos".send_message(
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
EXCEPTION
    WHEN foreign_key_violation THEN
        RAISE EXCEPTION 'DBOS non-existent workflow'
            USING DETAIL = format('Destination workflow %s does not exist', destination_id),
                ERRCODE = 'foreign_key_violation';
END;
$$ LANGUAGE plpgsql;
UPDATE "dbos".dbos_migrations SET version = 14;
-- Migration 15
-- Migration 15: Add columns to workflow_schedules for enhanced scheduling
-- Adds last_fired_at, automatic_backfill, and cron_timezone columns.

ALTER TABLE "dbos".workflow_schedules ADD COLUMN "last_fired_at" TEXT DEFAULT NULL;
ALTER TABLE "dbos".workflow_schedules ADD COLUMN "automatic_backfill" BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE "dbos".workflow_schedules ADD COLUMN "cron_timezone" TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 15;
-- Migration 16
-- Migration 16: Add delay_until_epoch_ms column for delayed queue execution
-- Workflows with this set start in DELAYED status and transition to ENQUEUED after the delay expires.

ALTER TABLE "dbos".workflow_status ADD COLUMN "delay_until_epoch_ms" BIGINT DEFAULT NULL;
CREATE INDEX "idx_workflow_status_delayed" ON "dbos".workflow_status ("delay_until_epoch_ms") WHERE status = 'DELAYED';
UPDATE "dbos".dbos_migrations SET version = 16;
-- Migration 17
-- Migration 17: Add queue_name column to workflow_schedules
-- Lets schedules target a user-defined queue; NULL falls back to the internal queue.

ALTER TABLE "dbos".workflow_schedules ADD COLUMN "queue_name" TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 17;
-- Migration 18
-- Migration 18: Add was_forked_from column to workflow_status. Marks workflows
-- that have been forked at least once, so the fork query can avoid scanning
-- rows that have never been a fork source.

ALTER TABLE "dbos"."workflow_status" ADD COLUMN "was_forked_from" BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE "dbos".dbos_migrations SET version = 18;
-- Migration 19
-- Migration 19: Index operation_outputs on (completed_at_epoch_ms, function_name)
-- to speed up queries that filter or sort step outputs by completion time.

CREATE INDEX "idx_operation_outputs_completed_at_function_name" ON "dbos"."operation_outputs" ("completed_at_epoch_ms", "function_name");
UPDATE "dbos".dbos_migrations SET version = 19;
-- Migration 20
-- Migration 20: Pin search_path on the plpgsql functions installed by the
-- earlier migrations. Without this, a function's search_path resolves at
-- call time against whatever the caller has set, which lets an attacker with
-- CREATE-on-schema privileges shadow built-ins. Setting it to
-- pg_catalog, pg_temp ensures references resolve only to the system catalog.
--
-- This migration is skipped on CockroachDB, which does not support
-- ALTER FUNCTION ... SET. The runner passes the empty string in that case.

ALTER FUNCTION "dbos".enqueue_workflow(
    TEXT, TEXT, JSON[], JSON, TEXT, TEXT, TEXT, TEXT, BIGINT, BIGINT, TEXT, INT4, TEXT
) SET search_path = pg_catalog, pg_temp;

ALTER FUNCTION "dbos".send_message(
    TEXT, JSON, TEXT, TEXT
) SET search_path = pg_catalog, pg_temp;

ALTER FUNCTION "dbos".notifications_function() SET search_path = pg_catalog, pg_temp;
ALTER FUNCTION "dbos".workflow_events_function() SET search_path = pg_catalog, pg_temp;
UPDATE "dbos".dbos_migrations SET version = 20;
-- Migration 21
-- Migration 21: Create the queues registry table. Persists per-queue
-- configuration (concurrency limits, rate limits, priority, partitioning,
-- polling cadence) so it can be inspected and updated independently of any
-- particular running executor.

CREATE TABLE "dbos".queues (
    queue_id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
    name TEXT NOT NULL UNIQUE,
    concurrency INT4,
    worker_concurrency INT4,
    rate_limit_max INT4,
    rate_limit_period_sec DOUBLE PRECISION,
    priority_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    partition_queue BOOLEAN NOT NULL DEFAULT FALSE,
    polling_interval_sec DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    created_at BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * 1000.0)::bigint,
    updated_at BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * 1000.0)::bigint
);
UPDATE "dbos".dbos_migrations SET version = 21;
-- Migration 22
-- Migration 18: Drop the non-partial index on forked_from in preparation for
-- recreating it as a partial index (only on non-NULL values).

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."idx_workflow_status_forked_from";
UPDATE "dbos".dbos_migrations SET version = 22;
-- Migration 23
-- Migration 19: Recreate idx_workflow_status_forked_from as a partial index.
-- The full index was rarely useful since most workflows have a NULL forked_from
-- value, so this avoids index maintenance on every workflow insert.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_forked_from" ON "dbos"."workflow_status" ("forked_from") WHERE "forked_from" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 23;
-- Migration 24
-- Migration 20: Drop the non-partial index on parent_workflow_id in preparation
-- for recreating it as a partial index (only on non-NULL values).

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."idx_workflow_status_parent_workflow_id";
UPDATE "dbos".dbos_migrations SET version = 24;
-- Migration 25
-- Migration 21: Recreate idx_workflow_status_parent_workflow_id as a partial
-- index. The full index was rarely useful since most workflows have a NULL
-- parent_workflow_id, so this avoids index maintenance on every workflow insert.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_parent_workflow_id" ON "dbos"."workflow_status" ("parent_workflow_id") WHERE "parent_workflow_id" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 25;
-- Migration 26
-- Migration 22: Drop the index on executor_id. The recovery query that used
-- this index no longer relies on it.

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."workflow_status_executor_id_index";
UPDATE "dbos".dbos_migrations SET version = 26;
-- Migration 27
-- Migration 23: Create a partial unique index on (queue_name, deduplication_id)
-- restricted to non-NULL deduplication_id. The new index uses a different name
-- than the original unique constraint (uq_workflow_status_queue_name_dedup_id)
-- to avoid a naming collision; the old constraint is dropped in migration 24.

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS "uq_workflow_status_dedup_id" ON "dbos"."workflow_status" ("queue_name", "deduplication_id") WHERE "deduplication_id" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 27;
-- Migration 28
-- Migration 24: Drop the original full unique constraint on
-- (queue_name, deduplication_id). The partial-index replacement
-- (uq_workflow_status_dedup_id) was created in migration 23.
--
-- This is a fast catalog operation, so CONCURRENTLY is not needed.
-- CockroachDB implements unique constraints as indexes and rejects
-- ALTER TABLE DROP CONSTRAINT for them; PostgreSQL rejects DROP INDEX
-- on a constraint-backed index. The runner picks the right statement
-- based on the dialect.

ALTER TABLE "dbos".workflow_status DROP CONSTRAINT IF EXISTS uq_workflow_status_queue_name_dedup_id;
UPDATE "dbos".dbos_migrations SET version = 28;
-- Migration 29
-- Migration 25: Create a partial index on workflow_status restricted to
-- PENDING workflows. Used by the recovery query. Splitting the old
-- workflow_status_status_index into two partial indexes (this and
-- idx_workflow_status_failed) reduces the number of times the index is
-- updated during a typical workflow lifetime
-- (ENQUEUED -> PENDING -> SUCCESS only updates this index on entry/exit
-- of PENDING).

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_pending" ON "dbos"."workflow_status" ("created_at") WHERE "status" = 'PENDING';
UPDATE "dbos".dbos_migrations SET version = 29;
-- Migration 30
-- Migration 26: Create a partial index on workflow_status restricted to
-- failed workflows (ERROR, CANCELLED, MAX_RECOVERY_ATTEMPTS_EXCEEDED) for
-- rapid troubleshooting queries.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_failed" ON "dbos"."workflow_status" ("status", "created_at") WHERE "status" IN ('ERROR', 'CANCELLED', 'MAX_RECOVERY_ATTEMPTS_EXCEEDED');
UPDATE "dbos".dbos_migrations SET version = 30;
-- Migration 31
-- Migration 27: Drop the original full index on workflow_status.status.
-- Replaced by the two partial indexes idx_workflow_status_pending and
-- idx_workflow_status_failed.

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."workflow_status_status_index";
UPDATE "dbos".dbos_migrations SET version = 31;
-- Migration 32
-- Migration 28: Create a partial composite index used by the main dequeue
-- query. Indexes on (queue_name, status, priority, created_at) — the exact
-- predicates and sort order of the dequeue query — restricted to in-flight
-- (ENQUEUED or PENDING) workflows.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_in_flight" ON "dbos"."workflow_status" ("queue_name", "status", "priority", "created_at") WHERE "status" IN ('ENQUEUED', 'PENDING');
UPDATE "dbos".dbos_migrations SET version = 32;
-- Migration 33
-- Migration 29: Add a rate_limited column to workflow_status. Workflows
-- dequeued from a queue with a rate limiter are marked rate_limited = TRUE
-- so the rate-limiter count can use a partial index restricted to those rows
-- (see idx_workflow_status_rate_limited in migration 30).
--
-- ALTER TABLE ADD COLUMN with a constant default is fast on Postgres because
-- it is a catalog-only update (attmissingval), so CONCURRENTLY is not needed.

ALTER TABLE "dbos"."workflow_status" ADD COLUMN IF NOT EXISTS "rate_limited" BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE "dbos".dbos_migrations SET version = 33;
-- Migration 34
-- Migration 30: Create a partial index used by the rate-limiter count query
-- to quickly find recent workflows in a rate-limited queue. The index is only
-- maintained for workflows actually dequeued under a rate limiter.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_rate_limited" ON "dbos"."workflow_status" ("queue_name", "started_at_epoch_ms") WHERE "rate_limited" = TRUE;
UPDATE "dbos".dbos_migrations SET version = 34;
-- Migration 35
-- Migration 31: Drop the original queue-status-started index. Superseded by
-- the partial in-flight index (idx_workflow_status_in_flight) and the partial
-- rate-limited index (idx_workflow_status_rate_limited).

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."idx_workflow_status_queue_status_started";
UPDATE "dbos".dbos_migrations SET version = 35;
-- Migration 36
-- Migration 36: Add completed_at column to workflow_status, recording when a
-- workflow reached a terminal state. ADD COLUMN with no default is catalog-only;
-- the partial index built in the same transaction covers zero rows, so no
-- CONCURRENTLY is needed.

ALTER TABLE "dbos"."workflow_status" ADD COLUMN IF NOT EXISTS "completed_at" BIGINT;
CREATE INDEX IF NOT EXISTS "idx_workflow_status_completed_at" ON "dbos"."workflow_status" ("completed_at") WHERE "completed_at" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 36;
-- Migration 37
-- Migration 37: Create a partial index on started_at_epoch_ms to speed up
-- queries filtering workflows by dequeue time.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_started_at" ON "dbos"."workflow_status" ("started_at_epoch_ms") WHERE "started_at_epoch_ms" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 37;
-- Migration 38
-- Migration 38: Replace enqueue_workflow with a signature that also accepts
-- authenticated_user, authenticated_roles, and delay_until_epoch_ms. A
-- non-NULL delay enqueues the workflow in the DELAYED state. The old 13-arg
-- function is dropped first so only the new signature remains.

DROP FUNCTION IF EXISTS "dbos".enqueue_workflow(
    TEXT, TEXT, JSON[], JSON, TEXT, TEXT, TEXT, TEXT, BIGINT, BIGINT, TEXT, INT4, TEXT
);

CREATE OR REPLACE FUNCTION "dbos".enqueue_workflow(
    workflow_name TEXT,
    queue_name TEXT,
    positional_args JSON[] DEFAULT ARRAY[]::JSON[],
    named_args JSON DEFAULT '{}'::JSON,
    class_name TEXT DEFAULT NULL,
    config_name TEXT DEFAULT NULL,
    workflow_id TEXT DEFAULT NULL,
    app_version TEXT DEFAULT NULL,
    timeout_ms BIGINT DEFAULT NULL,
    deadline_epoch_ms BIGINT DEFAULT NULL,
    deduplication_id TEXT DEFAULT NULL,
    priority INT4 DEFAULT NULL,
    queue_partition_key TEXT DEFAULT NULL,
    authenticated_user TEXT DEFAULT NULL,
    authenticated_roles TEXT DEFAULT NULL,
    delay_until_epoch_ms BIGINT DEFAULT NULL
) RETURNS TEXT AS $$
DECLARE
    v_workflow_id TEXT;
    v_serialized_inputs TEXT;
    v_owner_xid TEXT;
    v_now BIGINT;
    v_recovery_attempts INT4 := 0;
    v_priority INT4;
    v_status TEXT;
BEGIN

    -- Validate required parameters
    IF workflow_name IS NULL OR workflow_name = '' THEN
        RAISE EXCEPTION 'Workflow name cannot be null or empty';
    END IF;
    IF queue_name IS NULL OR queue_name = '' THEN
        RAISE EXCEPTION 'Queue name cannot be null or empty';
    END IF;
    IF named_args IS NOT NULL AND jsonb_typeof(named_args::jsonb) != 'object' THEN
        RAISE EXCEPTION 'Named args must be a JSON object';
    END IF;
    IF workflow_id IS NOT NULL AND workflow_id = '' THEN
        RAISE EXCEPTION 'Workflow ID cannot be an empty string if provided.';
    END IF;
    IF delay_until_epoch_ms IS NOT NULL AND delay_until_epoch_ms < 0 THEN
        RAISE EXCEPTION 'delay_until_epoch_ms must be >= 0';
    END IF;

    v_workflow_id := COALESCE(workflow_id, gen_random_uuid()::TEXT);
    v_owner_xid := gen_random_uuid()::TEXT;
    v_priority := COALESCE(priority, 0);
    v_serialized_inputs := json_build_object(
        'positionalArgs', positional_args,
        'namedArgs', named_args
    )::TEXT;
    v_now := EXTRACT(epoch FROM now()) * 1000;
    v_status := CASE WHEN delay_until_epoch_ms IS NULL THEN 'ENQUEUED' ELSE 'DELAYED' END;

    INSERT INTO "dbos".workflow_status (
        workflow_uuid, status, inputs,
        name, class_name, config_name,
        queue_name, deduplication_id, priority, queue_partition_key,
        application_version,
        created_at, updated_at, recovery_attempts,
        workflow_timeout_ms, workflow_deadline_epoch_ms,
        parent_workflow_id, owner_xid, serialization,
        authenticated_user, authenticated_roles,
        delay_until_epoch_ms
    ) VALUES (
        v_workflow_id, v_status, v_serialized_inputs,
        workflow_name, class_name, config_name,
        queue_name, deduplication_id, v_priority, queue_partition_key,
        app_version,
        v_now, v_now, v_recovery_attempts,
        timeout_ms, deadline_epoch_ms,
        NULL, v_owner_xid, 'portable_json',
        authenticated_user, authenticated_roles,
        delay_until_epoch_ms
    )
    ON CONFLICT (workflow_uuid)
    DO UPDATE SET
        updated_at = EXCLUDED.updated_at;

    RETURN v_workflow_id;

EXCEPTION
    WHEN unique_violation THEN
        RAISE EXCEPTION 'DBOS queue duplicated'
            USING DETAIL = format('Workflow %s with queue %s and deduplication ID %s already exists', v_workflow_id, queue_name, deduplication_id),
                ERRCODE = 'unique_violation';
END;
$$ LANGUAGE plpgsql;

-- Migration 38 (Postgres-only tail): pin search_path on the new
-- enqueue_workflow overload, matching the hardening applied in migration 20.
-- Skipped on CockroachDB, which does not support ALTER FUNCTION ... SET.

ALTER FUNCTION "dbos".enqueue_workflow(
    TEXT, TEXT, JSON[], JSON, TEXT, TEXT, TEXT, TEXT, BIGINT, BIGINT, TEXT, INT4, TEXT, TEXT, TEXT, BIGINT
) SET search_path = pg_catalog, pg_temp;
UPDATE "dbos".dbos_migrations SET version = 38;
-- Migration 39
-- Migration 39: Notify a blocked stream reader as soon as a value is written,
-- mirroring the notifications/workflow_events triggers from migration 1. Gated
-- on LISTEN/NOTIFY support; deployments without it (e.g. CockroachDB) use the
-- polling fallback and skip this migration.

CREATE OR REPLACE FUNCTION "dbos".streams_function() RETURNS TRIGGER AS $$
DECLARE
    payload text := NEW.workflow_uuid || '::' || NEW.key;
BEGIN
    PERFORM pg_notify('dbos_streams_channel', payload);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

ALTER FUNCTION "dbos".streams_function() SET search_path = pg_catalog, pg_temp;

DROP TRIGGER IF EXISTS dbos_streams_trigger ON "dbos".streams;
CREATE TRIGGER dbos_streams_trigger
AFTER INSERT ON "dbos".streams
FOR EACH ROW EXECUTE FUNCTION "dbos".streams_function();
UPDATE "dbos".dbos_migrations SET version = 39;
-- Migration 40
-- Migration 40: Add a JSONB attributes column to workflow_status for arbitrary
-- user-supplied workflow metadata. ADD COLUMN with no default is catalog-only;
-- the partial index built in the same transaction covers zero rows, so no
-- CONCURRENTLY is needed. The index supports containment (@>) filters; on
-- CockroachDB, USING GIN creates an inverted index.

ALTER TABLE "dbos"."workflow_status" ADD COLUMN IF NOT EXISTS "attributes" JSONB;
CREATE INDEX IF NOT EXISTS "idx_workflow_status_attributes" ON "dbos"."workflow_status" USING GIN ("attributes") WHERE "attributes" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 40;
-- Migration 41
-- Migration 41: Add a schedule_name column to workflow_status recording which
-- named schedule (if any) enqueued the workflow. ADD COLUMN with no default is
-- catalog-only; the partial index built in the same transaction covers zero
-- rows (no existing row has a non-NULL schedule_name), so no CONCURRENTLY is
-- needed. The index supports filtering workflows by schedule name.

ALTER TABLE "dbos"."workflow_status" ADD COLUMN IF NOT EXISTS "schedule_name" TEXT;
CREATE INDEX IF NOT EXISTS "idx_workflow_status_schedule_name" ON "dbos"."workflow_status" ("schedule_name") WHERE "schedule_name" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 41;
-- Migration 42
-- Migration 42: Add debounce columns to workflow_status. A debounced workflow
-- is enqueued DELAYED holding its debounce key as its deduplication_id; each
-- bounce extends delay_until_epoch_ms, capped at debounce_deadline_epoch_ms.
-- is_debounced marks the deduplication ID as a debounce key to clear on the
-- DELAYED->ENQUEUED transition. ADD COLUMN with a constant default is
-- catalog-only, so no CONCURRENTLY is needed.

ALTER TABLE "dbos"."workflow_status" ADD COLUMN IF NOT EXISTS "debounce_deadline_epoch_ms" BIGINT DEFAULT NULL;
ALTER TABLE "dbos"."workflow_status" ADD COLUMN IF NOT EXISTS "is_debounced" BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE "dbos".dbos_migrations SET version = 42;
-- Migration 43
-- Migration 43: Drop the per-row streams NOTIFY trigger installed by migration
-- 39. A notifying commit takes a global lock on the async notification queue,
-- so notifying from inside the write transaction serializes stream writes.
-- Notifications are now coalesced in memory and pushed off the write path by
-- the notifier loop. Postgres-only, matching migration 39.

DROP TRIGGER IF EXISTS dbos_streams_trigger ON "dbos".streams;
DROP FUNCTION IF EXISTS "dbos".streams_function();
UPDATE "dbos".dbos_migrations SET version = 43;
-- Migration 44
-- Migration 44: Drop the per-row workflow_events NOTIFY trigger installed by
-- migration 1, for the same reason as migration 43: SetEvent notifications are
-- now coalesced and pushed off the write path by the notifier loop.
--
-- The notifications trigger (DBOS.Send) is deliberately kept: messages can be
-- sent from anywhere, including processes with no notifier loop to flush them.

DROP TRIGGER IF EXISTS dbos_workflow_events_trigger ON "dbos".workflow_events;
DROP FUNCTION IF EXISTS "dbos".workflow_events_function();
UPDATE "dbos".dbos_migrations SET version = 44;
-- Migration 45
-- Migration 45: Create the partitioned-queue dequeue index. Extends
-- idx_workflow_status_in_flight with queue_partition_key so lookups scoped to
-- one partition stay selective when many partitions are active.
-- Superseded by idx_workflow_status_partition_dequeue_v2 (migration 46) and
-- dropped by migration 47.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_partition_dequeue" ON "dbos"."workflow_status" ("queue_name", "status", "queue_partition_key", "priority", "created_at") WHERE "status" IN ('ENQUEUED', 'PENDING') AND "queue_partition_key" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 45;
-- Migration 46
-- Migration 46: Recreate the partitioned-queue dequeue index with a trailing
-- workflow_uuid, which totalizes the dequeue order so per-partition head
-- probes are pure top-1 index seeks. Supersedes
-- idx_workflow_status_partition_dequeue (migration 45), dropped by migration 47.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_partition_dequeue_v2" ON "dbos"."workflow_status" ("queue_name", "status", "queue_partition_key", "priority", "created_at", "workflow_uuid") WHERE "status" IN ('ENQUEUED', 'PENDING') AND "queue_partition_key" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 46;
-- Migration 47
-- Migration 47: Drop the v1 partitioned-queue dequeue index (migration 45),
-- superseded by idx_workflow_status_partition_dequeue_v2 (migration 46).

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."idx_workflow_status_partition_dequeue";
UPDATE "dbos".dbos_migrations SET version = 47;
-- Migration 100
-- Migration 100: Add application_name to workflow_status, the first migration
-- of the cross-SDK shared history (see SharedMigrationBase). NULL means
-- unclaimed: any application may read and claim the row. One table per
-- migration, so a blocked table does not hold the others' locks.

ALTER TABLE "dbos"."workflow_status" ADD COLUMN IF NOT EXISTS "application_name" TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 100;
-- Migration 101
-- Migration 101: Add application_name to queues. NULL means unclaimed.

ALTER TABLE "dbos"."queues" ADD COLUMN IF NOT EXISTS "application_name" TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 101;
-- Migration 102
-- Migration 102: Add application_name to workflow_schedules. NULL means
-- unclaimed.

ALTER TABLE "dbos"."workflow_schedules" ADD COLUMN IF NOT EXISTS "application_name" TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 102;
-- Migration 103
-- Migration 103: Add application_name to application_versions. NULL means
-- unclaimed.

ALTER TABLE "dbos"."application_versions" ADD COLUMN IF NOT EXISTS "application_name" TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 103;
-- Migration 104
-- Migration 104: Add application_name to operation_outputs, denormalized from
-- the parent workflow so step observability filters without a join.

ALTER TABLE "dbos"."operation_outputs" ADD COLUMN IF NOT EXISTS "application_name" TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 104;
-- Migration 105
-- Migration 105: Replace enqueue_workflow with a signature that also accepts
-- a trailing application_name. Callers omitting it (SDKs predating the
-- feature) enqueue an unclaimed workflow. The old 16-arg function is dropped
-- first so only the new signature remains.

DROP FUNCTION IF EXISTS "dbos".enqueue_workflow(
    TEXT, TEXT, JSON[], JSON, TEXT, TEXT, TEXT, TEXT, BIGINT, BIGINT, TEXT, INT4, TEXT, TEXT, TEXT, BIGINT
);

CREATE OR REPLACE FUNCTION "dbos".enqueue_workflow(
    workflow_name TEXT,
    queue_name TEXT,
    positional_args JSON[] DEFAULT ARRAY[]::JSON[],
    named_args JSON DEFAULT '{}'::JSON,
    class_name TEXT DEFAULT NULL,
    config_name TEXT DEFAULT NULL,
    workflow_id TEXT DEFAULT NULL,
    app_version TEXT DEFAULT NULL,
    timeout_ms BIGINT DEFAULT NULL,
    deadline_epoch_ms BIGINT DEFAULT NULL,
    deduplication_id TEXT DEFAULT NULL,
    priority INT4 DEFAULT NULL,
    queue_partition_key TEXT DEFAULT NULL,
    authenticated_user TEXT DEFAULT NULL,
    authenticated_roles TEXT DEFAULT NULL,
    delay_until_epoch_ms BIGINT DEFAULT NULL,
    application_name TEXT DEFAULT NULL
) RETURNS TEXT AS $$
DECLARE
    v_workflow_id TEXT;
    v_serialized_inputs TEXT;
    v_owner_xid TEXT;
    v_now BIGINT;
    v_recovery_attempts INT4 := 0;
    v_priority INT4;
    v_status TEXT;
BEGIN

    -- Validate required parameters
    IF workflow_name IS NULL OR workflow_name = '' THEN
        RAISE EXCEPTION 'Workflow name cannot be null or empty';
    END IF;
    IF queue_name IS NULL OR queue_name = '' THEN
        RAISE EXCEPTION 'Queue name cannot be null or empty';
    END IF;
    IF named_args IS NOT NULL AND jsonb_typeof(named_args::jsonb) != 'object' THEN
        RAISE EXCEPTION 'Named args must be a JSON object';
    END IF;
    IF workflow_id IS NOT NULL AND workflow_id = '' THEN
        RAISE EXCEPTION 'Workflow ID cannot be an empty string if provided.';
    END IF;
    IF delay_until_epoch_ms IS NOT NULL AND delay_until_epoch_ms < 0 THEN
        RAISE EXCEPTION 'delay_until_epoch_ms must be >= 0';
    END IF;

    v_workflow_id := COALESCE(workflow_id, gen_random_uuid()::TEXT);
    v_owner_xid := gen_random_uuid()::TEXT;
    v_priority := COALESCE(priority, 0);
    v_serialized_inputs := json_build_object(
        'positionalArgs', positional_args,
        'namedArgs', named_args
    )::TEXT;
    v_now := EXTRACT(epoch FROM now()) * 1000;
    v_status := CASE WHEN delay_until_epoch_ms IS NULL THEN 'ENQUEUED' ELSE 'DELAYED' END;

    INSERT INTO "dbos".workflow_status (
        workflow_uuid, status, inputs,
        name, class_name, config_name,
        queue_name, deduplication_id, priority, queue_partition_key,
        application_version,
        created_at, updated_at, recovery_attempts,
        workflow_timeout_ms, workflow_deadline_epoch_ms,
        parent_workflow_id, owner_xid, serialization,
        authenticated_user, authenticated_roles,
        delay_until_epoch_ms, application_name
    ) VALUES (
        v_workflow_id, v_status, v_serialized_inputs,
        workflow_name, class_name, config_name,
        queue_name, deduplication_id, v_priority, queue_partition_key,
        app_version,
        v_now, v_now, v_recovery_attempts,
        timeout_ms, deadline_epoch_ms,
        NULL, v_owner_xid, 'portable_json',
        authenticated_user, authenticated_roles,
        delay_until_epoch_ms, application_name
    )
    ON CONFLICT (workflow_uuid)
    DO UPDATE SET
        updated_at = EXCLUDED.updated_at;

    RETURN v_workflow_id;

EXCEPTION
    WHEN unique_violation THEN
        RAISE EXCEPTION 'DBOS queue duplicated'
            USING DETAIL = format('Workflow %s with queue %s and deduplication ID %s already exists', v_workflow_id, queue_name, deduplication_id),
                ERRCODE = 'unique_violation';
END;
$$ LANGUAGE plpgsql;

-- Migration 105 (Postgres-only tail): pin search_path on the new
-- enqueue_workflow overload, matching the hardening applied in migration 20.
-- Skipped on CockroachDB, which does not support ALTER FUNCTION ... SET.

ALTER FUNCTION "dbos".enqueue_workflow(
    TEXT, TEXT, JSON[], JSON, TEXT, TEXT, TEXT, TEXT, BIGINT, BIGINT, TEXT, INT4, TEXT, TEXT, TEXT, BIGINT, TEXT
) SET search_path = pg_catalog, pg_temp;
UPDATE "dbos".dbos_migrations SET version = 105;
-- Migration 106
-- Migration 106: With 107, the pair of keys replacing version_name's retiring
-- global uniqueness, unclaimed counting as its own owner. The old
-- version_name unique constraint may not be dropped until every SDK reaching
-- this database is past 107.

CREATE UNIQUE INDEX IF NOT EXISTS "uq_application_versions_owner_version" ON "dbos"."application_versions" ("application_name", "version_name") WHERE "application_name" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 106;
-- Migration 107
-- Migration 107: The unclaimed half of the key pair started in migration 106.
-- Runs online because every pre-upgrade row is unclaimed, so the index covers
-- them all.

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS "uq_application_versions_unclaimed_version" ON "dbos"."application_versions" ("version_name") WHERE "application_name" IS NULL;
UPDATE "dbos".dbos_migrations SET version = 107;
-- Migration 108
-- Migration 108: Per-partition limits on queues. Any of these being set
-- partitions the queue; each applies per partition. ADD COLUMN with a constant
-- default is catalog-only, so no CONCURRENTLY is needed.

ALTER TABLE "dbos"."queues" ADD COLUMN IF NOT EXISTS "partition_concurrency" INT4 DEFAULT NULL;
ALTER TABLE "dbos"."queues" ADD COLUMN IF NOT EXISTS "partition_worker_concurrency" INT4 DEFAULT NULL;
ALTER TABLE "dbos"."queues" ADD COLUMN IF NOT EXISTS "partition_rate_limit_max" INT4 DEFAULT NULL;
ALTER TABLE "dbos"."queues" ADD COLUMN IF NOT EXISTS "partition_rate_limit_period_sec" DOUBLE PRECISION DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 108;
-- Migration 109
-- Migration 109: workflow inputs and outputs move to their own tables.

CREATE TABLE IF NOT EXISTS "dbos"."workflow_input" (
    workflow_uuid TEXT NOT NULL PRIMARY KEY,
    inputs TEXT,
    retention_timestamp BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * 1000.0)::bigint
);

CREATE TABLE IF NOT EXISTS "dbos"."workflow_output" (
    workflow_uuid TEXT NOT NULL PRIMARY KEY,
    output TEXT,
    error TEXT,
    retention_timestamp BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * 1000.0)::bigint
);

CREATE INDEX IF NOT EXISTS "idx_workflow_input_retention"
    ON "dbos"."workflow_input" ("retention_timestamp");

CREATE INDEX IF NOT EXISTS "idx_workflow_output_retention"
    ON "dbos"."workflow_output" ("retention_timestamp");
UPDATE "dbos".dbos_migrations SET version = 109;
-- Migration 110
-- Migration 110: sweep order for the step payload sweep.

ALTER TABLE "dbos"."operation_outputs"
    ADD COLUMN IF NOT EXISTS "retention_timestamp" BIGINT NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * 1000.0)::bigint;
UPDATE "dbos".dbos_migrations SET version = 110;
-- Migration 111
-- Migration 111: index the step sweep order, online.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_operation_outputs_retention"
    ON "dbos"."operation_outputs" ("retention_timestamp");
UPDATE "dbos".dbos_migrations SET version = 111;
-- Migration 112
-- Migration 112: steps no longer cascade from workflow_status.

ALTER TABLE "dbos"."operation_outputs"
    DROP CONSTRAINT IF EXISTS "operation_outputs_workflow_uuid_foreign";

ALTER TABLE "dbos"."operation_outputs"
    DROP CONSTRAINT IF EXISTS "operation_outputs_workflow_uuid_fkey";
UPDATE "dbos".dbos_migrations SET version = 112;
-- Migration 113
-- Migration 113: enqueue_workflow writes inputs to workflow_input.

CREATE OR REPLACE FUNCTION "dbos".enqueue_workflow(
    workflow_name TEXT,
    queue_name TEXT,
    positional_args JSON[] DEFAULT ARRAY[]::JSON[],
    named_args JSON DEFAULT '{}'::JSON,
    class_name TEXT DEFAULT NULL,
    config_name TEXT DEFAULT NULL,
    workflow_id TEXT DEFAULT NULL,
    app_version TEXT DEFAULT NULL,
    timeout_ms BIGINT DEFAULT NULL,
    deadline_epoch_ms BIGINT DEFAULT NULL,
    deduplication_id TEXT DEFAULT NULL,
    priority INT4 DEFAULT NULL,
    queue_partition_key TEXT DEFAULT NULL,
    authenticated_user TEXT DEFAULT NULL,
    authenticated_roles TEXT DEFAULT NULL,
    delay_until_epoch_ms BIGINT DEFAULT NULL,
    application_name TEXT DEFAULT NULL
) RETURNS TEXT AS $$
DECLARE
    v_workflow_id TEXT;
    v_serialized_inputs TEXT;
    v_owner_xid TEXT;
    v_now BIGINT;
    v_recovery_attempts INT4 := 0;
    v_priority INT4;
    v_status TEXT;
BEGIN

    -- Validate required parameters
    IF workflow_name IS NULL OR workflow_name = '' THEN
        RAISE EXCEPTION 'Workflow name cannot be null or empty';
    END IF;
    IF queue_name IS NULL OR queue_name = '' THEN
        RAISE EXCEPTION 'Queue name cannot be null or empty';
    END IF;
    IF named_args IS NOT NULL AND jsonb_typeof(named_args::jsonb) != 'object' THEN
        RAISE EXCEPTION 'Named args must be a JSON object';
    END IF;
    IF workflow_id IS NOT NULL AND workflow_id = '' THEN
        RAISE EXCEPTION 'Workflow ID cannot be an empty string if provided.';
    END IF;
    IF delay_until_epoch_ms IS NOT NULL AND delay_until_epoch_ms < 0 THEN
        RAISE EXCEPTION 'delay_until_epoch_ms must be >= 0';
    END IF;

    v_workflow_id := COALESCE(workflow_id, gen_random_uuid()::TEXT);
    v_owner_xid := gen_random_uuid()::TEXT;
    v_priority := COALESCE(priority, 0);
    v_serialized_inputs := json_build_object(
        'positionalArgs', positional_args,
        'namedArgs', named_args
    )::TEXT;
    v_now := EXTRACT(epoch FROM now()) * 1000;
    v_status := CASE WHEN delay_until_epoch_ms IS NULL THEN 'ENQUEUED' ELSE 'DELAYED' END;

    INSERT INTO "dbos".workflow_status (
        workflow_uuid, status,
        name, class_name, config_name,
        queue_name, deduplication_id, priority, queue_partition_key,
        application_version,
        created_at, updated_at, recovery_attempts,
        workflow_timeout_ms, workflow_deadline_epoch_ms,
        parent_workflow_id, owner_xid, serialization,
        authenticated_user, authenticated_roles,
        delay_until_epoch_ms, application_name
    ) VALUES (
        v_workflow_id, v_status,
        workflow_name, class_name, config_name,
        queue_name, deduplication_id, v_priority, queue_partition_key,
        app_version,
        v_now, v_now, v_recovery_attempts,
        timeout_ms, deadline_epoch_ms,
        NULL, v_owner_xid, 'portable_json',
        authenticated_user, authenticated_roles,
        delay_until_epoch_ms, application_name
    )
    ON CONFLICT (workflow_uuid)
    DO UPDATE SET
        updated_at = EXCLUDED.updated_at;

    INSERT INTO "dbos".workflow_input (
        workflow_uuid, inputs, retention_timestamp
    ) VALUES (
        v_workflow_id, v_serialized_inputs, v_now
    )
    ON CONFLICT (workflow_uuid) DO NOTHING;

    RETURN v_workflow_id;

EXCEPTION
    WHEN unique_violation THEN
        RAISE EXCEPTION 'DBOS queue duplicated'
            USING DETAIL = format('Workflow %s with queue %s and deduplication ID %s already exists', v_workflow_id, queue_name, deduplication_id),
                ERRCODE = 'unique_violation';
END;
$$ LANGUAGE plpgsql;

-- Migration 113 (Postgres-only tail): re-pin search_path on enqueue_workflow.

ALTER FUNCTION "dbos".enqueue_workflow(
    TEXT, TEXT, JSON[], JSON, TEXT, TEXT, TEXT, TEXT, BIGINT, BIGINT, TEXT, INT4, TEXT, TEXT, TEXT, BIGINT, TEXT
) SET search_path = pg_catalog, pg_temp;
UPDATE "dbos".dbos_migrations SET version = 113;
-- Migration 114
-- Migration 114: Drop idx_notifications (migration 12). It duplicates
-- idx_workflow_topic, which covers the same columns in the same order.

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."idx_notifications";
UPDATE "dbos".dbos_migrations SET version = 114;
-- Migration 115
-- Migration 115: Recreate the main dequeue index with application_name INCLUDEd.
-- INCLUDE, not a key column: app-scoped counts then run index-only, while the
-- planner still cannot BitmapOr on application_name. Supersedes
-- idx_workflow_status_in_flight (migration 32), dropped by migration 116.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_in_flight_v2" ON "dbos"."workflow_status" ("queue_name", "status", "priority", "created_at") INCLUDE ("application_name") WHERE "status" IN ('ENQUEUED', 'PENDING');
UPDATE "dbos".dbos_migrations SET version = 115;
-- Migration 116
-- Migration 116: Drop the v1 dequeue index (migration 32), superseded by
-- idx_workflow_status_in_flight_v2 (migration 115).

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."idx_workflow_status_in_flight";
UPDATE "dbos".dbos_migrations SET version = 116;
-- Migration 117
-- Migration 117: Recreate the partitioned-queue dequeue index with
-- application_name INCLUDEd, as idx_workflow_status_in_flight_v2 does.
-- Supersedes idx_workflow_status_partition_dequeue_v2 (migration 46), dropped
-- by migration 118.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_partition_dequeue_v3" ON "dbos"."workflow_status" ("queue_name", "status", "queue_partition_key", "priority", "created_at", "workflow_uuid") INCLUDE ("application_name") WHERE "status" IN ('ENQUEUED', 'PENDING') AND "queue_partition_key" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 117;
-- Migration 118
-- Migration 118: Drop the v2 partitioned-queue dequeue index (migration 46),
-- superseded by idx_workflow_status_partition_dequeue_v3 (migration 117).

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."idx_workflow_status_partition_dequeue_v2";
UPDATE "dbos".dbos_migrations SET version = 118;
-- Migration 119
-- Migration 119: Recreate the step-completion index with application_name
-- INCLUDEd, as idx_workflow_status_in_flight_v2 does. Supersedes
-- idx_operation_outputs_completed_at_function_name (migration 19), dropped by
-- migration 120.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_operation_outputs_completed_at_function_name_v2" ON "dbos"."operation_outputs" ("completed_at_epoch_ms", "function_name") INCLUDE ("application_name");
UPDATE "dbos".dbos_migrations SET version = 119;
-- Migration 120
-- Migration 120: Drop the v1 step-completion index (migration 19), superseded
-- by idx_operation_outputs_completed_at_function_name_v2 (migration 119).

DROP INDEX CONCURRENTLY IF EXISTS "dbos"."idx_operation_outputs_completed_at_function_name";
UPDATE "dbos".dbos_migrations SET version = 120;
-- Migration 121
-- Migration 121: Record which recv consumed a notification, so a rewind can
-- delete the messages the discarded run took delivery of.

ALTER TABLE "dbos"."notifications"
    ADD COLUMN IF NOT EXISTS "consumed_by_function_id" INT4;
UPDATE "dbos".dbos_migrations SET version = 121;
-- Migration 122
-- Migration 122: Partial index for the workflow timeouts monitor.

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_workflow_status_deadline" ON "dbos"."workflow_status" ("workflow_deadline_epoch_ms") WHERE "status" IN ('ENQUEUED', 'PENDING', 'DELAYED') AND "workflow_deadline_epoch_ms" IS NOT NULL;
UPDATE "dbos".dbos_migrations SET version = 122;
-- Migration 123
-- Migration 123: Record the token of the insert that created the row, so
-- owner_xid can carry the token of the execution that currently owns it.

ALTER TABLE "dbos"."workflow_status"
    ADD COLUMN IF NOT EXISTS "creator_xid" TEXT DEFAULT NULL;
UPDATE "dbos".dbos_migrations SET version = 123;
-- END dbos migrate --print-migrations 1

-- The grants of `dbos migrate --print-user-role`, given to the `fan-api` IAM
-- user only. The workflow worker runs as it, and so does the fan API, the only
-- workload that starts workflows so far. Workflow inputs carry business data,
-- so no other role gets them through a `%@%.iam` loop (see the grant
-- convention in 20261008020000_restrict_organizer_console_api_and_zitadel_app_grants.sql);
-- a workload that starts workflows later grants its own role by name.
-- The default privileges cover the tables that later DBOS migrations create,
-- because the Atlas Operator runs every migration as the same postgres user.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  -- Cloud SQL registers IAM service-account users as `<name>@<project>.iam`;
  -- the loop skips the role where it does not exist yet.
  FOR iam_role IN SELECT rolname FROM pg_roles WHERE rolname LIKE 'fan-api@%.iam' LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA "dbos" TO %I', iam_role);
    EXECUTE format('GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA "dbos" TO %I', iam_role);
    EXECUTE format('GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA "dbos" TO %I', iam_role);
    EXECUTE format('GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA "dbos" TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA "dbos" GRANT ALL ON TABLES TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA "dbos" GRANT ALL ON SEQUENCES TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA "dbos" GRANT EXECUTE ON FUNCTIONS TO %I', iam_role);
  END LOOP;
END
$$;
