-- Modify "events" table
ALTER TABLE "events" DROP CONSTRAINT "uq_events_natural_key", ADD CONSTRAINT "uq_events_natural_key" UNIQUE ("artist_id", "local_event_date");
COMMENT ON CONSTRAINT "uq_events_natural_key" ON "events" IS 'Prevents duplicate events for the same artist and date. An artist cannot perform at two venues simultaneously.';
