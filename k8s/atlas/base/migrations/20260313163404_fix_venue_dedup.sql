-- Delete all concert/event/venue data for a clean start.
-- The next discovery cron run will re-populate with deduplicated data.
DELETE FROM concerts;
DELETE FROM events;
DELETE FROM venues;

-- Modify "events" table: add artist_id and change natural key from (venue_id, date, start_at) to (artist_id, date, start_at)
ALTER TABLE "events" DROP CONSTRAINT "uq_events_natural_key", ADD COLUMN "artist_id" uuid NOT NULL, ADD CONSTRAINT "uq_events_natural_key" UNIQUE NULLS NOT DISTINCT ("artist_id", "local_event_date", "start_at"), ADD CONSTRAINT "events_artist_id_fkey" FOREIGN KEY ("artist_id") REFERENCES "artists" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Set comment to column: "artist_id" on table: "events"
COMMENT ON COLUMN "events"."artist_id" IS 'Reference to the performing artist; denormalized from concerts for natural key deduplication';
-- Update constraint comment
COMMENT ON CONSTRAINT "uq_events_natural_key" ON "events" IS 'Prevents duplicate events for the same artist, date, and start time. NULLS NOT DISTINCT treats two NULL start_at values as equal.';
