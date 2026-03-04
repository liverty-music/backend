-- Modify "latest_search_logs" table
ALTER TABLE "app"."latest_search_logs" ADD COLUMN "status" text NOT NULL DEFAULT 'completed';
-- Set comment to column: "status" on table: "latest_search_logs"
COMMENT ON COLUMN "app"."latest_search_logs"."status" IS 'Search job status: pending, completed, or failed';
