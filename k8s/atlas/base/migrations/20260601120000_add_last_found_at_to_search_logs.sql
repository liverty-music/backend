-- Modify "latest_search_logs" table
ALTER TABLE "latest_search_logs" ADD COLUMN "last_found_at" timestamptz NULL;
-- Set comment to column: "last_found_at" on table: "latest_search_logs"
COMMENT ON COLUMN "latest_search_logs"."last_found_at" IS 'Timestamp of the most recent search that discovered at least one new concert; NULL if none ever found';
