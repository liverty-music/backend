-- Create "staged_concerts" table
CREATE TABLE "staged_concerts" (
  "id" uuid NOT NULL,
  "artist_id" uuid NOT NULL,
  "title" text NOT NULL,
  "local_date" date NOT NULL,
  "start_at" timestamptz NULL,
  "open_at" timestamptz NULL,
  "listed_venue_name" text NOT NULL,
  "admin_area" text NULL,
  "source_url" text NULL,
  "resolved_place_id" text NULL,
  "resolved_venue_name" text NULL,
  "resolved_admin_area" text NULL,
  "resolved_latitude" double precision NULL,
  "resolved_longitude" double precision NULL,
  "discovered_at" timestamptz NOT NULL DEFAULT NOW(),
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_staged_concerts_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'),
  CONSTRAINT "staged_concerts_artist_id_fkey" FOREIGN KEY ("artist_id") REFERENCES "artists" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Set comment to table: "staged_concerts"
COMMENT ON TABLE "staged_concerts" IS 'Approval queue for AI-discovered concerts. Holds only pending rows; approve publishes and deletes, reject logs and deletes. Re-discovery dedup consults this table plus published events, but never the rejection log.';
-- Set comment to column: "id" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."id" IS 'Unique staged concert identifier (UUIDv7, application-generated). Exposed to the admin console as StagedConcertId.';
-- Set comment to column: "artist_id" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."artist_id" IS 'The performing artist this concert was discovered for.';
-- Set comment to column: "title" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."title" IS 'Descriptive title extracted for the concert (e.g. tour or show name).';
-- Set comment to column: "local_date" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."local_date" IS 'Scheduled calendar date of the concert in the venue local timezone.';
-- Set comment to column: "start_at" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."start_at" IS 'Scheduled start time. NULL when the source did not state one.';
-- Set comment to column: "open_at" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."open_at" IS 'Doors-open time. NULL when not announced.';
-- Set comment to column: "listed_venue_name" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."listed_venue_name" IS 'Raw venue name exactly as scraped from the source, preserved for review.';
-- Set comment to column: "admin_area" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."admin_area" IS 'Administrative area extracted by Gemini for the concert. NULL when not extracted.';
-- Set comment to column: "source_url" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."source_url" IS 'Source URL where the concert was found. NULL when not provided.';
-- Set comment to column: "resolved_place_id" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."resolved_place_id" IS 'Google Places place id of the resolved venue. NULL when the listed name could not be resolved.';
-- Set comment to column: "resolved_venue_name" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."resolved_venue_name" IS 'Canonical venue name resolved via Google Places. NULL when unresolved.';
-- Set comment to column: "resolved_admin_area" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."resolved_admin_area" IS 'ISO 3166-2 admin area of the resolved venue. NULL when unresolved or indeterminate.';
-- Set comment to column: "resolved_latitude" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."resolved_latitude" IS 'WGS 84 latitude of the resolved venue. NULL when unresolved.';
-- Set comment to column: "resolved_longitude" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."resolved_longitude" IS 'WGS 84 longitude of the resolved venue. NULL when unresolved.';
-- Set comment to column: "discovered_at" on table: "staged_concerts"
COMMENT ON COLUMN "staged_concerts"."discovered_at" IS 'Timestamp when the discovery pipeline staged this concert. Used to order the review queue.';
-- Create index "idx_staged_concerts_discovered_at" to table: "staged_concerts"
CREATE INDEX "idx_staged_concerts_discovered_at" ON "staged_concerts" ("discovered_at");
-- Set comment to index: "idx_staged_concerts_discovered_at" on table: "staged_concerts"
COMMENT ON INDEX "idx_staged_concerts_discovered_at" IS 'Orders the review queue by discovery time';
-- Create index "uq_staged_concerts_by_listed_name" to table: "staged_concerts"
CREATE UNIQUE INDEX "uq_staged_concerts_by_listed_name" ON "staged_concerts" ("artist_id", "local_date", "listed_venue_name") WHERE (resolved_place_id IS NULL);
-- Set comment to index: "uq_staged_concerts_by_listed_name" on table: "staged_concerts"
COMMENT ON INDEX "uq_staged_concerts_by_listed_name" IS 'Natural-key dedup fallback when the venue did not resolve: one pending row per (artist, date, raw listed name).';
-- Create index "uq_staged_concerts_by_place" to table: "staged_concerts"
CREATE UNIQUE INDEX "uq_staged_concerts_by_place" ON "staged_concerts" ("artist_id", "local_date", "resolved_place_id") WHERE (resolved_place_id IS NOT NULL);
-- Set comment to index: "uq_staged_concerts_by_place" on table: "staged_concerts"
COMMENT ON INDEX "uq_staged_concerts_by_place" IS 'Natural-key dedup for resolved venues: one pending row per (artist, date, place id).';
-- Create "rejected_concerts_log" table
CREATE TABLE "rejected_concerts_log" (
  "id" uuid NOT NULL,
  "artist_id" uuid NOT NULL,
  "artist_name" text NOT NULL,
  "title" text NOT NULL,
  "local_date" date NOT NULL,
  "start_at" timestamptz NULL,
  "open_at" timestamptz NULL,
  "listed_venue_name" text NOT NULL,
  "admin_area" text NULL,
  "source_url" text NULL,
  "resolved_place_id" text NULL,
  "resolved_venue_name" text NULL,
  "resolved_admin_area" text NULL,
  "reason" text NOT NULL,
  "reviewed_by" text NULL,
  "rejected_at" timestamptz NOT NULL DEFAULT NOW(),
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_rejected_concerts_log_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7')
);
-- Set comment to table: "rejected_concerts_log"
COMMENT ON TABLE "rejected_concerts_log" IS 'Append-only audit of rejected staged concerts, used for searcher-quality analysis only. Not consulted by discovery dedup; never suppresses re-discovery.';
-- Set comment to column: "id" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."id" IS 'Unique log entry identifier (UUIDv7, application-generated).';
-- Set comment to column: "artist_id" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."artist_id" IS 'The performing artist the rejected concert was discovered for. Intentionally not a foreign key so the log survives artist deletion.';
-- Set comment to column: "artist_name" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."artist_name" IS 'Artist display name captured at rejection time for readability.';
-- Set comment to column: "title" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."title" IS 'Descriptive title of the rejected concert.';
-- Set comment to column: "local_date" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."local_date" IS 'Scheduled calendar date of the rejected concert.';
-- Set comment to column: "start_at" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."start_at" IS 'Scheduled start time of the rejected concert. NULL when unknown.';
-- Set comment to column: "open_at" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."open_at" IS 'Doors-open time of the rejected concert. NULL when unknown.';
-- Set comment to column: "listed_venue_name" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."listed_venue_name" IS 'Raw scraped venue name of the rejected concert.';
-- Set comment to column: "admin_area" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."admin_area" IS 'Administrative area extracted for the rejected concert. NULL when not extracted.';
-- Set comment to column: "source_url" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."source_url" IS 'Source URL of the rejected concert. NULL when not provided.';
-- Set comment to column: "resolved_place_id" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."resolved_place_id" IS 'Google Places place id of the resolved venue at rejection time. NULL when unresolved.';
-- Set comment to column: "resolved_venue_name" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."resolved_venue_name" IS 'Resolved canonical venue name at rejection time. NULL when unresolved.';
-- Set comment to column: "resolved_admin_area" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."resolved_admin_area" IS 'Resolved admin area at rejection time. NULL when unresolved.';
-- Set comment to column: "reason" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."reason" IS 'Reviewer-provided reason for rejecting the concert.';
-- Set comment to column: "reviewed_by" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."reviewed_by" IS 'Identity (Zitadel subject) of the developer who rejected the concert. NULL when unavailable.';
-- Set comment to column: "rejected_at" on table: "rejected_concerts_log"
COMMENT ON COLUMN "rejected_concerts_log"."rejected_at" IS 'Timestamp when the concert was rejected.';
-- Create index "idx_rejected_concerts_log_artist_id" to table: "rejected_concerts_log"
CREATE INDEX "idx_rejected_concerts_log_artist_id" ON "rejected_concerts_log" ("artist_id");
-- Set comment to index: "idx_rejected_concerts_log_artist_id" on table: "rejected_concerts_log"
COMMENT ON INDEX "idx_rejected_concerts_log_artist_id" IS 'Supports per-artist analysis of repeated rejection patterns';
-- Create index "idx_rejected_concerts_log_rejected_at" to table: "rejected_concerts_log"
CREATE INDEX "idx_rejected_concerts_log_rejected_at" ON "rejected_concerts_log" ("rejected_at");
-- Set comment to index: "idx_rejected_concerts_log_rejected_at" on table: "rejected_concerts_log"
COMMENT ON INDEX "idx_rejected_concerts_log_rejected_at" IS 'Supports time-windowed analysis of rejections';
