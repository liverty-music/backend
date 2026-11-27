-- Modify "events" table
ALTER TABLE "events" ADD COLUMN "other_listed_venue_names" text[] NOT NULL DEFAULT '{}';
-- Set comment to column: "other_listed_venue_names" on table: "events"
COMMENT ON COLUMN "events"."other_listed_venue_names" IS 'Other venue names, normalized, that the event is also listed under: names an admin did not choose when resolving a duplicate, names replaced by another, and names of events merged into this one. A later discovery under one of them is a re-discovery of this event.';
