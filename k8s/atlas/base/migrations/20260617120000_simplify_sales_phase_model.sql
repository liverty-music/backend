-- Drop "event_sales_phases" table (per-event coverage subset removed; sales phases are now series-level)
DROP TABLE "event_sales_phases";
-- Modify "sales_phases" table: drop the covered-event anchor column
ALTER TABLE "sales_phases" DROP COLUMN "anchor_event_id";
