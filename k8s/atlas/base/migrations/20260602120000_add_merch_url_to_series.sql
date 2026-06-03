-- Modify "series" table
ALTER TABLE "series" ADD COLUMN "merch_url" text NULL;
-- Set comment to column: "merch_url" on table: "series"
COMMENT ON COLUMN "series"."merch_url" IS 'Optional official merchandise information page (official site page or official social media post) shared across the series; populated asynchronously by the merch-url discovery job. Stores only the link — no sale timing, channel, price, or item data.';
