-- Official resale (official-resale, design D8): resale_listings, the
-- resale open time of a ticket type and the resale flag of a checkout. No grant
-- is written: the read-write roles' default privileges give fan-api full CRUD,
-- the SELECT default privilege of organizer-console-api covers the new table,
-- and reception-api gets nothing.

SET search_path TO app, public;

-- Modify "checkouts" table
ALTER TABLE "checkouts" ADD COLUMN "resale" boolean NOT NULL DEFAULT false;
-- Set comment to column: "resale" on table: "checkouts"
COMMENT ON COLUMN "checkouts"."resale" IS 'True for a resale checkout: it holds resale_listings rows of its ticket type instead of the ticket type''s stock, and never changes sold_count. Fixed at creation';
-- Modify "ticket_types" table
ALTER TABLE "ticket_types" ADD COLUMN "resale_open_at" timestamptz NULL;
-- Set comment to column: "resale_open_at" on table: "ticket_types"
COMMENT ON COLUMN "ticket_types"."resale_open_at" IS 'When official resale opened: the commit time that first made sold_count equal quantity (first-come only). NULL until then; set once and never cleared, even when the quantity is raised or a commit is reverted';
-- Create "resale_listings" table
CREATE TABLE "resale_listings" (
  "id" uuid NOT NULL,
  "ticket_id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "ticket_type_id" uuid NOT NULL,
  "price" bigint NOT NULL,
  "status" smallint NOT NULL,
  "checkout_id" uuid NULL,
  "created_at" timestamptz NOT NULL,
  "sold_at" timestamptz NULL,
  "withdraw_at" timestamptz NULL,
  "provider_refund_id" text NULL,
  "refund_at" timestamptz NULL,
  "sold_email_send_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "resale_listings_checkout_id_fkey" FOREIGN KEY ("checkout_id") REFERENCES "checkouts" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "resale_listings_ticket_id_fkey" FOREIGN KEY ("ticket_id") REFERENCES "tickets" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "resale_listings_ticket_type_id_fkey" FOREIGN KEY ("ticket_type_id") REFERENCES "ticket_types" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "chk_resale_listings_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_resale_listings_price" CHECK ((price >= 1) AND (price <= 1000000)),
  CONSTRAINT "chk_resale_listings_provider_refund_id_not_empty" CHECK ((provider_refund_id IS NULL) OR (provider_refund_id <> ''::text)),
  CONSTRAINT "chk_resale_listings_refund_together" CHECK ((provider_refund_id IS NULL) = (refund_at IS NULL)),
  CONSTRAINT "chk_resale_listings_refund_when_sold" CHECK ((refund_at IS NULL) OR (status = 2)),
  CONSTRAINT "chk_resale_listings_sold_email_when_sold" CHECK ((sold_email_send_at IS NULL) OR (status = 2)),
  CONSTRAINT "chk_resale_listings_sold_time" CHECK ((status = 2) = (sold_at IS NOT NULL)),
  CONSTRAINT "chk_resale_listings_status" CHECK ((status >= 1) AND (status <= 4)),
  CONSTRAINT "chk_resale_listings_withdraw_time" CHECK ((status = 3) = (withdraw_at IS NOT NULL))
);
-- Create index "idx_resale_listings_checkout_id" to table: "resale_listings"
CREATE INDEX "idx_resale_listings_checkout_id" ON "resale_listings" ("checkout_id") WHERE (checkout_id IS NOT NULL);
-- Create index "idx_resale_listings_listed_by_ticket_type" to table: "resale_listings"
CREATE INDEX "idx_resale_listings_listed_by_ticket_type" ON "resale_listings" ("ticket_type_id", "created_at") WHERE (status = 1);
-- Create index "idx_resale_listings_sold_unfinished" to table: "resale_listings"
CREATE INDEX "idx_resale_listings_sold_unfinished" ON "resale_listings" ("sold_at") WHERE ((status = 2) AND ((refund_at IS NULL) OR (sold_email_send_at IS NULL)));
-- Create index "idx_resale_listings_user_id" to table: "resale_listings"
CREATE INDEX "idx_resale_listings_user_id" ON "resale_listings" ("user_id", "created_at");
-- Create index "uq_resale_listings_listed_ticket" to table: "resale_listings"
CREATE UNIQUE INDEX "uq_resale_listings_listed_ticket" ON "resale_listings" ("ticket_id") WHERE (status = 1);
-- Set comment to table: "resale_listings"
COMMENT ON TABLE "resale_listings" IS 'A fan''s offer of one ticket for official resale (公式リセール) at face value. Status 1=Listed, 2=Sold, 3=Withdrawn, 4=Cancelled; Expired is derived when read (Listed at or after 3 hours before the event start) and never stored. The resale fee (10% of price, rounded down) and the refund amount (price minus fee) are derived from price.';
-- Set comment to column: "id" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."id" IS 'Unique listing identifier (UUIDv7, application-generated)';
-- Set comment to column: "ticket_id" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."ticket_id" IS 'The ticket offered; its order (tickets.order_id) is the one whose charge the user is refunded from';
-- Set comment to column: "user_id" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."user_id" IS 'The fan offering it: the ticket''s user (no FK, as for checkouts.user_id)';
-- Set comment to column: "ticket_type_id" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."ticket_type_id" IS 'The first-come ticket type the ticket was bought from (order → checkout → ticket type), resolved when listed';
-- Set comment to column: "price" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."price" IS 'Face value in yen, tax-inclusive (税込): the ticket type''s price when listed';
-- Set comment to column: "status" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."status" IS 'Lifecycle: 1=Listed, 2=Sold, 3=Withdrawn, 4=Cancelled';
-- Set comment to column: "checkout_id" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."checkout_id" IS 'The resale checkout that last held the listing; NULL until one holds it. The listing is free when that checkout is neither holding nor Committed';
-- Set comment to column: "created_at" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."created_at" IS 'When the fan listed the ticket; the oldest Listed listing is held first';
-- Set comment to column: "sold_at" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."sold_at" IS 'When another fan''s order bought it; set exactly when Sold';
-- Set comment to column: "withdraw_at" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."withdraw_at" IS 'When the fan withdrew it; set exactly when Withdrawn';
-- Set comment to column: "provider_refund_id" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."provider_refund_id" IS 'The payment provider''s ID for the refund of the refund amount to the listing''s user (e.g. Stripe "re_..."); set once together with refund_at';
-- Set comment to column: "refund_at" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."refund_at" IS 'When the listing''s user was refunded; set once together with provider_refund_id';
-- Set comment to column: "sold_email_send_at" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."sold_email_send_at" IS 'When the email telling the user the ticket sold was sent; set once right after a successful send';
-- Set comment to index: "idx_resale_listings_checkout_id" on table: "resale_listings"
COMMENT ON INDEX "idx_resale_listings_checkout_id" IS 'Optimizes reading the listings a resale checkout holds';
-- Set comment to index: "idx_resale_listings_listed_by_ticket_type" on table: "resale_listings"
COMMENT ON INDEX "idx_resale_listings_listed_by_ticket_type" IS 'Optimizes the oldest-first pick of free listings and the free resale count of a ticket type';
-- Set comment to index: "idx_resale_listings_sold_unfinished" on table: "resale_listings"
COMMENT ON INDEX "idx_resale_listings_sold_unfinished" IS 'Optimizes ListSoldUnrefunded: Sold listings not yet refunded or not yet emailed';
-- Set comment to index: "idx_resale_listings_user_id" on table: "resale_listings"
COMMENT ON INDEX "idx_resale_listings_user_id" IS 'Optimizes a fan''s listings, newest first';
-- Set comment to index: "uq_resale_listings_listed_ticket" on table: "resale_listings"
COMMENT ON INDEX "uq_resale_listings_listed_ticket" IS 'At most one Listed listing per ticket: a concurrent second listing loses with a unique violation';
