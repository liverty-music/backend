-- Migrate discovered sales (migrate-discovered-sales, design D2 and "Migration Plan").
--
-- A sale found by discovery is now a discovered TicketSale (discovered_at set,
-- no name, no ticket types). Every sales_phases row is copied with its id, the
-- reminder records and the search log move to ticket_sale_reminders and
-- ticket_sale_searches, and the stored notification type is renamed. The old
-- tables are dropped. Privileges: the read-write roles' default privileges
-- cover the new tables; the organizer and reception roles keep no access
-- beyond the SELECT their default privileges give.
--
-- Forward-only, in one transaction. Production held no sales phase and no
-- reminder record before this migration (read 2026-10-13).

SET search_path TO app, public;

-- Set comment to column: "type" on table: "notifications"
COMMENT ON COLUMN "notifications"."type" IS 'Notification type: new_concerts, sales_reminder, ticket_sale_announcement, order_confirmation';
-- Modify "ticket_sales" table
ALTER TABLE "ticket_sales" DROP CONSTRAINT "chk_ticket_sales_window", ADD CONSTRAINT "chk_ticket_sales_window" CHECK ((sale_end_at IS NULL) OR (sale_end_at > sale_start_at)), ADD CONSTRAINT "chk_ticket_sales_discovered" CHECK ((discovered_at IS NULL) OR ((verification_requirement = 0) AND (draw_at IS NULL))), ADD CONSTRAINT "chk_ticket_sales_end_required" CHECK ((sale_end_at IS NOT NULL) OR ((discovered_at IS NOT NULL) AND (method = 1))), ADD CONSTRAINT "chk_ticket_sales_lottery_result" CHECK ((lottery_result_at IS NULL) OR ((discovered_at IS NOT NULL) AND (method = 2) AND (lottery_result_at >= sale_end_at))), ADD CONSTRAINT "chk_ticket_sales_name_by_kind" CHECK ((discovered_at IS NULL) = (name IS NOT NULL)), ALTER COLUMN "sale_end_at" DROP NOT NULL, ALTER COLUMN "name" DROP NOT NULL, ADD COLUMN "lottery_result_at" timestamptz NULL, ADD COLUMN "discovered_at" timestamptz NULL, ADD COLUMN "sale_start_date_jst" date NULL GENERATED ALWAYS AS (((sale_start_at AT TIME ZONE 'Asia/Tokyo'::text))::date) STORED;
-- Create index "uq_ticket_sales_discovered_convergence" to table: "ticket_sales"
CREATE UNIQUE INDEX "uq_ticket_sales_discovered_convergence" ON "ticket_sales" ("series_id", "method", "sale_start_date_jst") WHERE (discovered_at IS NOT NULL);
-- Copy every sales phase into a discovered ticket sale with its id, so its
-- reminder records keep pointing at the same sale. The method is mapped by
-- name: SalesMethod is 1=LOTTERY, 2=FIRST_COME, TicketSaleMethod is
-- 1=FirstCome, 2=Lottery.
INSERT INTO "ticket_sales" ("id", "series_id", "name", "method", "sale_start_at", "sale_end_at",
                            "verification_requirement", "draw_at", "lottery_result_at", "discovered_at")
SELECT "id", "series_id", NULL,
       CASE "method" WHEN 1 THEN 2 WHEN 2 THEN 1 END,
       "apply_start_at", "apply_end_at", 0, NULL, "lottery_result_at", "discovered_at"
FROM "sales_phases";
-- Set comment to table: "ticket_sales"
COMMENT ON TABLE "ticket_sales" IS 'One sale opportunity (受付) over the events of one Series: an allocation method and a window. An Organizer''s sale (discovered_at NULL) also has a name and a verification requirement, and its per-event price, quantity and limit are ticket_types rows. A discovered sale (discovered_at set) belongs to a Series without an Organizer, has no name and no ticket types, and is only reported to fans.';
-- Set comment to column: "sale_end_at" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."sale_end_at" IS 'When the sale closes; after sale_start_at. On an Organizer''s sale required, 1 to 14 days after the start for a lottery, and before the start of every event the sale covers (checked by the application). NULL only on a discovered first-come sale that ends when tickets run out';
-- Set comment to column: "name" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."name" IS 'Sale name shown to fans and operators (1-100 characters), e.g. ファンクラブ先行 or 一般発売; required on an Organizer''s sale, NULL on a discovered sale';
-- Set comment to column: "verification_requirement" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."verification_requirement" IS '本人確認 requirement: 0=NONE (any fan), 1=VERIFIED_ANY (an Active IdentityVerification is required), 2=JPKI_ONLY (a JPKI-backed IdentityVerification is required); always 0 on a discovered sale';
-- Set comment to column: "draw_at" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."draw_at" IS 'When the lottery draw ran for every ticket type of the sale; NULL until the draw runs, for a first-come sale and for a discovered sale. Set atomically with the draw outcome; the draw sweeper skips sales where it is set.';
-- Set comment to column: "lottery_result_at" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."lottery_result_at" IS 'When the lottery result is announced (当落発表), as published on the official pages; a discovered lottery only, not before sale_end_at, NULL when not announced';
-- Set comment to column: "discovered_at" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."discovered_at" IS 'When discovery first stored the sale; set on insert and never updated. NULL exactly on an Organizer''s sale. The reminder scan uses it as the first-sight guard: a stage whose anchor is before discovered_at is not sent.';
-- Set comment to column: "sale_start_date_jst" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."sale_start_date_jst" IS 'Calendar date of sale_start_at in Asia/Tokyo; part of the discovered-sale convergence key';
-- Set comment to index: "uq_ticket_sales_discovered_convergence" on table: "ticket_sales"
COMMENT ON INDEX "uq_ticket_sales_discovered_convergence" IS 'One discovered sale per (series, method, start date in Japan time): re-discovery updates it in place. Partial, so an Organizer may open two sales of one Series on the same day for different events. Upserts infer it with ON CONFLICT (series_id, method, sale_start_date_jst) WHERE discovered_at IS NOT NULL.';
-- Create "ticket_sale_reminders" table
CREATE TABLE "ticket_sale_reminders" (
  "id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "ticket_sale_id" uuid NOT NULL,
  "stage" smallint NOT NULL,
  "sent_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "uq_ticket_sale_reminders" UNIQUE ("user_id", "ticket_sale_id", "stage"),
  CONSTRAINT "ticket_sale_reminders_ticket_sale_id_fkey" FOREIGN KEY ("ticket_sale_id") REFERENCES "ticket_sales" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "ticket_sale_reminders_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "chk_ticket_sale_reminders_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_ticket_sale_reminders_stage" CHECK ((stage >= 1) AND (stage <= 10))
);
-- Create index "idx_ticket_sale_reminders_ticket_sale_id" to table: "ticket_sale_reminders"
CREATE INDEX "idx_ticket_sale_reminders_ticket_sale_id" ON "ticket_sale_reminders" ("ticket_sale_id");
-- Create index "idx_ticket_sale_reminders_user_id" to table: "ticket_sale_reminders"
CREATE INDEX "idx_ticket_sale_reminders_user_id" ON "ticket_sale_reminders" ("user_id");
-- Set comment to table: "ticket_sale_reminders"
COMMENT ON TABLE "ticket_sale_reminders" IS 'Sent-log for ticket sale reminder notifications, for discovered and Organizer''s sales alike. UNIQUE (user_id, ticket_sale_id, stage) prevents duplicate dispatches.';
-- Set comment to column: "id" on table: "ticket_sale_reminders"
COMMENT ON COLUMN "ticket_sale_reminders"."id" IS 'Unique reminder record identifier (UUIDv7, application-generated)';
-- Set comment to column: "user_id" on table: "ticket_sale_reminders"
COMMENT ON COLUMN "ticket_sale_reminders"."user_id" IS 'Reference to the user who received the reminder';
-- Set comment to column: "ticket_sale_id" on table: "ticket_sale_reminders"
COMMENT ON COLUMN "ticket_sale_reminders"."ticket_sale_id" IS 'Reference to the ticket sale this reminder relates to';
-- Set comment to column: "stage" on table: "ticket_sale_reminders"
COMMENT ON COLUMN "ticket_sale_reminders"."stage" IS 'Reminder stage: 1=APPLY_OPEN (at the start for a lottery, 30 minutes before it for a first-come sale), 2=APPLY_CLOSE_24H (24h before the end, lottery only), 4=RESULT_DAY (09:00 on the day of the lottery''s result time). 3 was APPLY_CLOSE_1H and is unused.';
-- Set comment to column: "sent_at" on table: "ticket_sale_reminders"
COMMENT ON COLUMN "ticket_sale_reminders"."sent_at" IS 'Timestamp when the reminder was dispatched';
-- Set comment to index: "idx_ticket_sale_reminders_ticket_sale_id" on table: "ticket_sale_reminders"
COMMENT ON INDEX "idx_ticket_sale_reminders_ticket_sale_id" IS 'Optimizes lookup of all reminder records for a ticket sale';
-- Set comment to index: "idx_ticket_sale_reminders_user_id" on table: "ticket_sale_reminders"
COMMENT ON INDEX "idx_ticket_sale_reminders_user_id" IS 'Optimizes lookup of all reminders for a user';
-- Copy the reminder records; the sale ids are unchanged.
INSERT INTO "ticket_sale_reminders" ("id", "user_id", "ticket_sale_id", "stage", "sent_at")
SELECT "id", "user_id", "sales_phase_id", "stage", "sent_at" FROM "sales_phase_reminders";
-- Create "ticket_sale_searches" table
CREATE TABLE "ticket_sale_searches" (
  "series_id" uuid NOT NULL,
  "searched_at" timestamptz NOT NULL,
  PRIMARY KEY ("series_id"),
  CONSTRAINT "ticket_sale_searches_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Set comment to table: "ticket_sale_searches"
COMMENT ON TABLE "ticket_sale_searches" IS 'When each series was last searched for published ticket sales. One row per series.';
-- Set comment to column: "series_id" on table: "ticket_sale_searches"
COMMENT ON COLUMN "ticket_sale_searches"."series_id" IS 'The series that was searched';
-- Set comment to column: "searched_at" on table: "ticket_sale_searches"
COMMENT ON COLUMN "ticket_sale_searches"."searched_at" IS 'When the series was last searched successfully';
-- Copy the search log.
INSERT INTO "ticket_sale_searches" ("series_id", "searched_at")
SELECT "series_id", "searched_at" FROM "sales_phase_search_logs";
-- Rename the stored notification type.
UPDATE "notifications" SET "type" = 'ticket_sale_announcement' WHERE "type" = 'sales_phase_announcement';
-- Drop "sales_phase_reminders" table
DROP TABLE "sales_phase_reminders";
-- Drop "sales_phase_search_logs" table
DROP TABLE "sales_phase_search_logs";
-- Drop "sales_phases" table
DROP TABLE "sales_phases";
