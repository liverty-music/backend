-- Create "suppressed_concerts" table
CREATE TABLE "suppressed_concerts" (
  "id" uuid NOT NULL,
  "venue_id" uuid NOT NULL,
  "local_event_date" date NOT NULL,
  "start_at" timestamptz NULL,
  "suppressed_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "uq_suppressed_concerts_natural_key" UNIQUE NULLS NOT DISTINCT ("venue_id", "local_event_date", "start_at"),
  CONSTRAINT "chk_suppressed_concerts_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7')
);
COMMENT ON TABLE "suppressed_concerts" IS 'Moderation suppression set: the natural key of each published concert an operator deleted, consulted by discovery so a deleted concert is not auto-published or re-staged again. Distinct from rejected_concerts_log, which is analysis-only and never suppresses.';
COMMENT ON COLUMN "suppressed_concerts"."id" IS 'Unique suppression entry identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN "suppressed_concerts"."venue_id" IS 'Resolved venue of the deleted event. Intentionally not a foreign key so the suppression survives independently of venue lifecycle.';
COMMENT ON COLUMN "suppressed_concerts"."local_event_date" IS 'Local calendar date of the deleted event.';
COMMENT ON COLUMN "suppressed_concerts"."start_at" IS 'Start time of the deleted event. NULL when unknown; NULLS NOT DISTINCT collapses an unknown-start slot like the events unique key.';
COMMENT ON COLUMN "suppressed_concerts"."suppressed_at" IS 'Timestamp when the concert was suppressed (i.e. deleted by an operator).';
