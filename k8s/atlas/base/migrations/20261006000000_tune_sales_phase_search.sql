-- Simplify sales phases to method + milestones and add per-series search logs.
-- Part of tune-sales-phase-search (design D6, D7).
--
-- Every stored phase is deleted: many break the new rules (method UNSPECIFIED,
-- a lottery without a close), no reminder was ever sent for any of them, and
-- discovery finds the sales that are still upcoming again.
SET search_path TO app, public;

DELETE FROM "sales_phase_reminders";
DELETE FROM "sales_phases";

-- Drop the removed classification and detail columns with their checks.
ALTER TABLE "sales_phases"
  DROP CONSTRAINT "chk_sales_phases_method",
  DROP CONSTRAINT "chk_sales_phases_channel",
  DROP CONSTRAINT "chk_sales_phases_sequence",
  DROP COLUMN "channel",
  DROP COLUMN "provider_name",
  DROP COLUMN "sequence",
  DROP COLUMN "payment_deadline_at",
  DROP COLUMN "url";

-- Method is required, a lottery has a close, only a lottery has a result time,
-- and re-discovery converges on (series, method, apply start date in Japan time).
ALTER TABLE "sales_phases"
  ADD COLUMN "apply_start_date_jst" date NULL GENERATED ALWAYS AS (((apply_start_at AT TIME ZONE 'Asia/Tokyo'::text))::date) STORED,
  ADD CONSTRAINT "chk_sales_phases_method" CHECK (method = ANY (ARRAY[1, 2])),
  ADD CONSTRAINT "chk_sales_phases_result_only_lottery" CHECK ((method = 1) OR (lottery_result_at IS NULL)),
  ADD CONSTRAINT "chk_sales_phases_lottery_has_end" CHECK ((method = 2) OR (apply_end_at IS NOT NULL)),
  ADD CONSTRAINT "chk_sales_phases_end_after_start" CHECK ((apply_end_at IS NULL) OR (apply_end_at > apply_start_at)),
  ADD CONSTRAINT "uq_sales_phases_series_method_start_date" UNIQUE ("series_id", "method", "apply_start_date_jst");

COMMENT ON TABLE "sales_phases" IS 'A single series-level ticket-sales window. Re-discovered phases converge on (series_id, method, apply_start_date_jst).';
COMMENT ON COLUMN "sales_phases"."method" IS 'Sales method: 1=LOTTERY, 2=FIRST_COME';
COMMENT ON COLUMN "sales_phases"."apply_start_at" IS 'Start of the application or on-sale window (required).';
COMMENT ON COLUMN "sales_phases"."apply_end_at" IS 'End of the application window. Required for a lottery; NULL for a first-come sale that ends when tickets run out.';
COMMENT ON COLUMN "sales_phases"."lottery_result_at" IS 'When lottery results are announced. NULL for first-come phases or when not announced.';
COMMENT ON COLUMN "sales_phases"."apply_start_date_jst" IS 'Calendar date of apply_start_at in Asia/Tokyo; part of the re-discovery convergence key.';
COMMENT ON COLUMN "sales_phase_reminders"."stage" IS 'Reminder stage: 1=APPLY_OPEN (at apply_start_time for a lottery, 30 minutes before it for a first-come sale), 2=APPLY_CLOSE_24H (24h before apply_end_time, lottery only), 4=RESULT_DAY (09:00 on lottery_result_time day). 3 was APPLY_CLOSE_1H and is unused.';

-- When each series was last searched for sales phases.
CREATE TABLE "sales_phase_search_logs" (
  "series_id" uuid NOT NULL,
  "searched_at" timestamptz NOT NULL,
  PRIMARY KEY ("series_id"),
  CONSTRAINT "sales_phase_search_logs_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
COMMENT ON TABLE "sales_phase_search_logs" IS 'When each series was last searched for ticket sales phases. One row per series.';
COMMENT ON COLUMN "sales_phase_search_logs"."series_id" IS 'The series that was searched';
COMMENT ON COLUMN "sales_phase_search_logs"."searched_at" IS 'When the series was last searched successfully';
