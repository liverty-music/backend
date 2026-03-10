-- Step 1: Delete duplicate events, keeping the richest (non-NULL start_at preferred)
-- and earliest-inserted (smallest UUIDv7 id) row per natural key.
-- Corresponding concerts rows are cascade-deleted via FK.
DELETE FROM "events" e
USING (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY venue_id, local_event_date, start_at
               ORDER BY
                   CASE WHEN start_at IS NOT NULL THEN 0 ELSE 1 END,
                   id
           ) AS rn
    FROM "events"
) ranked
WHERE e.id = ranked.id AND ranked.rn > 1;

-- Step 2: Add UNIQUE constraint now that duplicates are removed
ALTER TABLE "events" ADD CONSTRAINT "uq_events_natural_key" UNIQUE NULLS NOT DISTINCT ("venue_id", "local_event_date", "start_at");
