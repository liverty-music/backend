-- Wipe events and everything cascading off it before reshaping the schema.
--
-- The legacy `events` row shape (with `artist_id`, `title`, `source_url`)
-- cannot be mechanically converted into the new `series_id` + M:N
-- `event_performers` shape: choosing how to group existing rows into series
-- requires curation that the migration cannot perform safely. Production has
-- not been released yet, so wiping is acceptable; dev environments will be
-- repopulated from Gemini auto-discovery on the next batch run.
TRUNCATE TABLE "events" CASCADE;

-- Create enum type "series_type"
CREATE TYPE "series_type" AS ENUM ('TOUR', 'SINGLE', 'FESTIVAL');
COMMENT ON TYPE "series_type" IS 'Classification of an event series: TOUR (multi-venue), SINGLE (single-venue standalone, possibly multi-day), FESTIVAL (multi-performer)';

-- Modify "concerts" table
ALTER TABLE "concerts" DROP COLUMN "artist_id";
-- Set comment to table: "concerts"
COMMENT ON TABLE "concerts" IS 'Music-specific event extension, linked 1:1 with events. Currently a placeholder; reserved for future music-specific columns per the Event-Type Extensibility requirement.';
-- Create "series" table
CREATE TABLE "series" (
  "id" uuid NOT NULL,
  "title" text NOT NULL,
  "type" "series_type" NOT NULL,
  "source_url" text NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_series_id_uuid_v5_or_v7" CHECK ("substring"((id)::text, 15, 1) = ANY (ARRAY['5'::text, '7'::text])),
  CONSTRAINT "chk_series_title_not_empty" CHECK (title <> ''::text)
);
-- Set comment to table: "series"
COMMENT ON TABLE "series" IS 'Parent aggregation above events. Owns metadata shared across every event in a tour, festival, or multi-day single-venue run.';
-- Set comment to column: "id" on table: "series"
COMMENT ON COLUMN "series"."id" IS 'Unique series identifier. UUIDv7 for synthetic (search-path) IDs; UUIDv5 for content-addressed deterministic IDs from auto-discovery (so a re-discovered concert produces the same series UUID and the events natural-key UPSERT deduplicates across runs).';
-- Set comment to column: "title" on table: "series"
COMMENT ON COLUMN "series"."title" IS 'Series title shared across all member events (e.g. tour name, festival name)';
-- Set comment to column: "type" on table: "series"
COMMENT ON COLUMN "series"."type" IS 'Classification of the series; drives presentation and notification grouping';
-- Set comment to column: "source_url" on table: "series"
COMMENT ON COLUMN "series"."source_url" IS 'Optional series-level official URL (tour page, festival page); per-event URLs are not stored';
-- Modify "events" table
ALTER TABLE "events" DROP CONSTRAINT "uq_events_natural_key", DROP COLUMN "title", DROP COLUMN "source_url", DROP COLUMN "artist_id", ADD COLUMN "series_id" uuid NOT NULL, ADD CONSTRAINT "uq_events_natural_key" UNIQUE ("series_id", "local_event_date", "venue_id"), ADD CONSTRAINT "events_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Create index "idx_events_series_id" to table: "events"
CREATE INDEX "idx_events_series_id" ON "events" ("series_id");
-- Set comment to table: "events"
COMMENT ON TABLE "events" IS 'A single performance occurring on a specific date at a specific venue. Belongs to exactly one parent series.';
-- Set comment to column: "series_id" on table: "events"
COMMENT ON COLUMN "events"."series_id" IS 'Reference to the parent series that aggregates this event with any sibling events';
-- Set comment to index: "idx_events_series_id" on table: "events"
COMMENT ON INDEX "idx_events_series_id" IS 'Optimizes listing all events belonging to a series';
-- Create "event_performers" table
CREATE TABLE "event_performers" (
  "event_id" uuid NOT NULL,
  "artist_id" uuid NOT NULL,
  PRIMARY KEY ("event_id", "artist_id"),
  CONSTRAINT "event_performers_artist_id_fkey" FOREIGN KEY ("artist_id") REFERENCES "artists" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "event_performers_event_id_fkey" FOREIGN KEY ("event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_event_performers_artist_id" to table: "event_performers"
CREATE INDEX "idx_event_performers_artist_id" ON "event_performers" ("artist_id");
-- Set comment to table: "event_performers"
COMMENT ON TABLE "event_performers" IS 'M:N relation between events and performing artists. Supports festival lineups, co-headliners, and support acts.';
-- Set comment to column: "event_id" on table: "event_performers"
COMMENT ON COLUMN "event_performers"."event_id" IS 'Reference to the event';
-- Set comment to column: "artist_id" on table: "event_performers"
COMMENT ON COLUMN "event_performers"."artist_id" IS 'Reference to the performing artist';
-- Set comment to index: "idx_event_performers_artist_id" on table: "event_performers"
COMMENT ON INDEX "idx_event_performers_artist_id" IS 'Optimizes lookup of all events for a given artist (reverse direction of the composite PK)';
