-- remodel-ticket-sales (design "Migration Plan", D1-D8, D12): SeatType and
-- TicketStock replace TicketType, an Order is created Pending when the fan
-- applies and carries what LotteryEntry and Checkout shared, the per-account
-- limit moves to the event, a sale names its events, and the draw time and
-- the lottery result time go.
--
-- No Organizer-sale row is moved. Every record of the Organizer sales is
-- deleted first; the read-only prod check on 2026-10-29 found 3 Organizer
-- sales, 4 Paid Orders with Held Settlements, 5 tickets and 3 resale listings,
-- all test data that the owner approved deleting as is (design "Pre-check
-- result"). Discovered sales are kept, except one without an end time, and
-- each is named by its method until discovery rereads its page; prod has none.
-- Recorded RESULT_DAY reminders are deleted with the stage.
--
-- The grants at the end give the organizer server the writes of SeatType,
-- TicketStock, the sale's events and the per-account limit. Its ticket_types
-- privileges go with the table; the other roles read the new tables through
-- their default privileges.

SET search_path TO app, public;

-- 1. Delete every record of the Organizer sales, children first. Only an
--    Organizer's sale has ticket types, and every lottery entry, checkout,
--    order, ticket, settlement and resale listing hangs off a ticket type, so
--    these tables are emptied whole; then the Organizer sales go (with their
--    reminders through the cascade).
DELETE FROM resale_listings;
DELETE FROM admissions;
DELETE FROM settlements;
DELETE FROM tickets;
DELETE FROM orders;
DELETE FROM checkouts;
DELETE FROM lottery_entries;
DELETE FROM ticket_types;
DELETE FROM ticket_sales WHERE discovered_at IS NULL;

-- 2. Discovered sales: every sale has a name and an end time, and the
--    RESULT_DAY stage (4) goes. Reminders of a deleted sale go with it.
DELETE FROM ticket_sales WHERE discovered_at IS NOT NULL AND sale_end_at IS NULL;
ALTER TABLE ticket_sales DROP CONSTRAINT chk_ticket_sales_name_by_kind;
UPDATE ticket_sales SET name = CASE method WHEN 2 THEN '抽選' ELSE '先着' END WHERE discovered_at IS NOT NULL AND name IS NULL;
DELETE FROM ticket_sale_reminders WHERE stage = 4;

-- 3. Schema changes. The per-account limit of every existing first-party
--    event is 4, the default.
-- Modify "events" table
ALTER TABLE "events" ADD COLUMN "per_account_limit" integer NULL;
UPDATE "events" SET "per_account_limit" = 4 WHERE "publish_state" IS NOT NULL;
ALTER TABLE "events" ADD CONSTRAINT "chk_events_per_account_limit" CHECK ((per_account_limit >= 1) AND (per_account_limit <= 10)), ADD CONSTRAINT "chk_events_per_account_limit_first_party" CHECK ((publish_state IS NULL) = (per_account_limit IS NULL));
-- Modify "ticket_sale_reminders" table
ALTER TABLE "ticket_sale_reminders" DROP CONSTRAINT "chk_ticket_sale_reminders_stage", ADD CONSTRAINT "chk_ticket_sale_reminders_stage" CHECK (stage = ANY (ARRAY[1, 2]));
-- Create "seat_types" table
CREATE TABLE "seat_types" (
  "id" uuid NOT NULL,
  "series_id" uuid NOT NULL,
  "name" text NOT NULL,
  "price" bigint NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "uq_seat_types_series_name" UNIQUE ("series_id", "name"),
  CONSTRAINT "seat_types_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "chk_seat_types_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_seat_types_name_len" CHECK ((char_length(name) >= 1) AND (char_length(name) <= 50)),
  CONSTRAINT "chk_seat_types_price" CHECK ((price >= 1) AND (price <= 1000000))
);
-- Create "ticket_stocks" table
CREATE TABLE "ticket_stocks" (
  "id" uuid NOT NULL,
  "event_id" uuid NOT NULL,
  "seat_type_id" uuid NOT NULL,
  "quantity" integer NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "uq_ticket_stocks_event_seat_type" UNIQUE ("event_id", "seat_type_id"),
  CONSTRAINT "ticket_stocks_event_id_fkey" FOREIGN KEY ("event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "ticket_stocks_seat_type_id_fkey" FOREIGN KEY ("seat_type_id") REFERENCES "seat_types" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "chk_ticket_stocks_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_ticket_stocks_quantity" CHECK (quantity >= 1)
);
-- Create index "idx_ticket_stocks_seat_type_id" to table: "ticket_stocks"
CREATE INDEX "idx_ticket_stocks_seat_type_id" ON "ticket_stocks" ("seat_type_id");
-- Modify "orders" table
ALTER TABLE "orders" DROP CONSTRAINT "chk_orders_one_source", DROP CONSTRAINT "chk_orders_status", ADD CONSTRAINT "chk_orders_status" CHECK (status = ANY (ARRAY[1, 2, 4, 5])), ADD CONSTRAINT "chk_orders_card_authorization_id_not_empty" CHECK ((card_authorization_id IS NULL) OR (card_authorization_id <> ''::text)), ADD CONSTRAINT "chk_orders_paid_card_authorization" CHECK ((status <> ALL (ARRAY[1, 2])) OR (card_authorization_id IS NOT NULL)), ADD CONSTRAINT "chk_orders_paid_payment_id" CHECK ((status = ANY (ARRAY[1, 2])) = (provider_payment_id IS NOT NULL)), ADD CONSTRAINT "chk_orders_paid_pay_time" CHECK ((status = ANY (ARRAY[1, 2])) = (pay_at IS NOT NULL)), ADD CONSTRAINT "chk_orders_provider_payment_id_not_empty" CHECK ((provider_payment_id IS NULL) OR (provider_payment_id <> ''::text)), ADD CONSTRAINT "chk_orders_referral_code" CHECK (referral_code ~ '^[a-z0-9-]{1,32}$'::text), ADD CONSTRAINT "chk_orders_ticket_count" CHECK ((ticket_count >= 1) AND (ticket_count <= 10)), DROP COLUMN "lottery_entry_id", ALTER COLUMN "provider_payment_id" DROP NOT NULL, ALTER COLUMN "pay_at" DROP NOT NULL, DROP COLUMN "checkout_id", ADD COLUMN "ticket_stock_id" uuid NOT NULL, ADD COLUMN "ticket_count" integer NOT NULL, ADD COLUMN "referral_code" text NULL, ADD COLUMN "card_authorization_id" text NULL, ADD CONSTRAINT "orders_ticket_stock_id_fkey" FOREIGN KEY ("ticket_stock_id") REFERENCES "ticket_stocks" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT;
-- Create index "idx_orders_ticket_stock_status" to table: "orders"
CREATE INDEX "idx_orders_ticket_stock_status" ON "orders" ("ticket_stock_id", "status");
-- Create index "uq_orders_card_authorization_id" to table: "orders"
CREATE UNIQUE INDEX "uq_orders_card_authorization_id" ON "orders" ("card_authorization_id") WHERE (card_authorization_id IS NOT NULL);
-- Modify "ticket_sales" table
ALTER TABLE "ticket_sales" DROP CONSTRAINT "chk_ticket_sales_draw_lottery_only", DROP CONSTRAINT "chk_ticket_sales_end_required", DROP CONSTRAINT "chk_ticket_sales_lottery_result", DROP CONSTRAINT "chk_ticket_sales_discovered", ADD CONSTRAINT "chk_ticket_sales_discovered" CHECK ((discovered_at IS NULL) OR (verification_requirement = 0)), DROP CONSTRAINT "chk_ticket_sales_window", ADD CONSTRAINT "chk_ticket_sales_window" CHECK (sale_end_at > sale_start_at), ALTER COLUMN "sale_end_at" SET NOT NULL, ALTER COLUMN "name" SET NOT NULL, DROP COLUMN "draw_at", DROP COLUMN "lottery_result_at";
-- Drop index "idx_checkouts_due" from table: "checkouts"
DROP INDEX "idx_checkouts_due";
-- Modify "checkouts" table
ALTER TABLE "checkouts" DROP CONSTRAINT "chk_checkouts_amount_positive", DROP CONSTRAINT "chk_checkouts_card_authorization_id_not_empty", DROP CONSTRAINT "chk_checkouts_referral_code", DROP CONSTRAINT "chk_checkouts_release_needs_card_authorization", DROP CONSTRAINT "chk_checkouts_ticket_count", DROP COLUMN "user_id", DROP COLUMN "ticket_count", DROP COLUMN "amount", DROP COLUMN "card_authorization_id", DROP COLUMN "ticket_type_id", DROP COLUMN "resale", DROP COLUMN "referral_code", ADD COLUMN "order_id" uuid NOT NULL, ADD COLUMN "ticket_sale_id" uuid NOT NULL, ADD CONSTRAINT "checkouts_order_id_fkey" FOREIGN KEY ("order_id") REFERENCES "orders" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT, ADD CONSTRAINT "checkouts_ticket_sale_id_fkey" FOREIGN KEY ("ticket_sale_id") REFERENCES "ticket_sales" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT;
-- Create index "idx_checkouts_due" to table: "checkouts"
CREATE INDEX "idx_checkouts_due" ON "checkouts" ("status", "hold_expire_at") WHERE ((status = ANY (ARRAY[1, 2])) OR ((status = ANY (ARRAY[4, 5])) AND (card_authorization_release_at IS NULL)));
-- Create index "idx_checkouts_ticket_sale_id" to table: "checkouts"
CREATE INDEX "idx_checkouts_ticket_sale_id" ON "checkouts" ("ticket_sale_id");
-- Create index "uq_checkouts_order_id" to table: "checkouts"
CREATE UNIQUE INDEX "uq_checkouts_order_id" ON "checkouts" ("order_id");
-- Modify "lottery_entries" table
ALTER TABLE "lottery_entries" DROP CONSTRAINT "chk_lottery_entries_card_authorization_id_not_empty", DROP CONSTRAINT "chk_lottery_entries_referral_code", DROP CONSTRAINT "chk_lottery_entries_ticket_count_positive", DROP COLUMN "ticket_type_id", DROP COLUMN "user_id", DROP COLUMN "ticket_count", DROP COLUMN "card_authorization_id", DROP COLUMN "referral_code", ADD COLUMN "order_id" uuid NOT NULL, ADD COLUMN "ticket_sale_id" uuid NOT NULL, ADD CONSTRAINT "lottery_entries_order_id_fkey" FOREIGN KEY ("order_id") REFERENCES "orders" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT, ADD CONSTRAINT "lottery_entries_ticket_sale_id_fkey" FOREIGN KEY ("ticket_sale_id") REFERENCES "ticket_sales" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT;
-- Create index "idx_lottery_entries_ticket_sale_state" to table: "lottery_entries"
CREATE INDEX "idx_lottery_entries_ticket_sale_state" ON "lottery_entries" ("ticket_sale_id", "state");
-- Create index "uq_lottery_entries_order_id" to table: "lottery_entries"
CREATE UNIQUE INDEX "uq_lottery_entries_order_id" ON "lottery_entries" ("order_id");
-- Modify "resale_listings" table
ALTER TABLE "resale_listings" DROP COLUMN "ticket_type_id", DROP COLUMN "checkout_id", ADD COLUMN "ticket_stock_id" uuid NOT NULL, ADD CONSTRAINT "resale_listings_ticket_stock_id_fkey" FOREIGN KEY ("ticket_stock_id") REFERENCES "ticket_stocks" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT;
-- Create index "idx_resale_listings_listed_by_ticket_stock" to table: "resale_listings"
CREATE INDEX "idx_resale_listings_listed_by_ticket_stock" ON "resale_listings" ("ticket_stock_id", "created_at") WHERE (status = 1);
-- Create "ticket_sale_events" table
CREATE TABLE "ticket_sale_events" (
  "ticket_sale_id" uuid NOT NULL,
  "event_id" uuid NOT NULL,
  PRIMARY KEY ("ticket_sale_id", "event_id"),
  CONSTRAINT "ticket_sale_events_event_id_fkey" FOREIGN KEY ("event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "ticket_sale_events_ticket_sale_id_fkey" FOREIGN KEY ("ticket_sale_id") REFERENCES "ticket_sales" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_ticket_sale_events_event_id" to table: "ticket_sale_events"
CREATE INDEX "idx_ticket_sale_events_event_id" ON "ticket_sale_events" ("event_id");
-- Drop "ticket_types" table
DROP TABLE "ticket_types";

-- 4. TicketStock.Delete removes the stock's Void orders with their checkouts
--    and lottery entries. The organizer server may never delete those itself
--    (migration_grants_integration_test "cannot alter evidence or fan
--    records"), so the delete runs in a SECURITY DEFINER function owned by the
--    migration role, and only the organizer server may execute it.
-- delete_ticket_stock removes a ticket stock with its Void orders and their
-- checkouts and lottery entries (TicketStock.Delete). It runs as its owner so
-- the organizer server, which may never delete orders, checkouts or lottery
-- entries itself, can remove only Void ones of a stock it deletes. It locks the
-- stock row first; an order inserted meanwhile takes a key-share lock on that
-- row through its foreign key, so it either is seen here or fails on the
-- missing stock. Returns false when no stock has the id. Raises
-- restrict_violation (23001) when an order that is not Void names the stock,
-- or when a Void order's checkout has not released its card authorization.
-- Its search_path names app and public: production keeps every table in app,
-- while a database migrated without the operator's search_path (CI's test-db)
-- has the older tables, orders and lottery_entries among them, in public.
CREATE OR REPLACE FUNCTION delete_ticket_stock(p_ticket_stock_id UUID) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY DEFINER SET search_path = app, public, pg_temp AS $$
BEGIN
  PERFORM 1 FROM ticket_stocks WHERE id = p_ticket_stock_id FOR UPDATE;
  IF NOT FOUND THEN
    RETURN FALSE;
  END IF;
  IF EXISTS (SELECT 1 FROM orders WHERE ticket_stock_id = p_ticket_stock_id AND status <> 5) THEN
    RAISE EXCEPTION 'ticket stock % is named by an order that is not void', p_ticket_stock_id USING ERRCODE = '23001';
  END IF;
  IF EXISTS (
    SELECT 1 FROM orders o JOIN checkouts c ON c.order_id = o.id
    WHERE o.ticket_stock_id = p_ticket_stock_id AND o.card_authorization_id IS NOT NULL AND c.card_authorization_release_at IS NULL
  ) THEN
    RAISE EXCEPTION 'ticket stock % has a void order whose card authorization is not released', p_ticket_stock_id USING ERRCODE = '23001';
  END IF;
  DELETE FROM checkouts WHERE order_id IN (SELECT id FROM orders WHERE ticket_stock_id = p_ticket_stock_id);
  DELETE FROM lottery_entries WHERE order_id IN (SELECT id FROM orders WHERE ticket_stock_id = p_ticket_stock_id);
  DELETE FROM orders WHERE ticket_stock_id = p_ticket_stock_id;
  DELETE FROM ticket_stocks WHERE id = p_ticket_stock_id;
  RETURN TRUE;
END
$$;
COMMENT ON FUNCTION delete_ticket_stock(UUID) IS 'TicketStock.Delete: removes a stock with its Void orders and their checkouts and lottery entries, refused (23001) while a live order names it or a Void order''s card authorization is not released. SECURITY DEFINER; EXECUTE is granted only to the organizer server.';
REVOKE ALL ON FUNCTION delete_ticket_stock(UUID) FROM PUBLIC;

-- 5. organizer-console-api writes of the new model (pattern of
--    20261010010000): SeatType Create and Delete, TicketStock Create (its
--    Delete goes through delete_ticket_stock), the sale's events at Create,
--    and each event's per-account limit through the Series RPCs.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('GRANT INSERT, DELETE ON seat_types TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON ticket_stocks TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON ticket_sale_events TO %I', iam_role);
    EXECUTE format('GRANT EXECUTE ON FUNCTION delete_ticket_stock(UUID) TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (per_account_limit) ON events TO %I', iam_role);
  END LOOP;
END
$$;

-- 6. Comments.
COMMENT ON COLUMN "events"."per_account_limit" IS 'Most tickets one account may hold for the event across every ticket sale, seat type and official resale of it: 1 to 10 (one AdmissionCode presents at most 10 tickets), 4 when the Organizer gives none. Set exactly on an event of a first-party series; fixed once the event is PUBLISHED (checked by the application)';
COMMENT ON COLUMN "settlements"."organizer_id" IS 'The Organizer that receives the payout. Stored denormalized so the sweeper avoids joining through the order''s ticket stock → event → series.';
COMMENT ON COLUMN "ticket_sale_reminders"."stage" IS 'Reminder stage: 1=APPLY_OPEN (at the start for a lottery, 30 minutes before it for a first-come sale), 2=APPLY_CLOSE_24H (24h before the end, lottery only). 3 was APPLY_CLOSE_1H and 4 was RESULT_DAY; neither is used.';
COMMENT ON TABLE "tickets" IS 'Account-bound covered tickets (特定興行入場券) issued when their order becomes Paid. Each carries the three covered-ticket conditions: resale-without-consent prohibited, date/venue+eligible-person (event_id + user_id, whose current full name and phone number the face shows), and bound 本人確認.';
COMMENT ON TABLE "seat_types" IS 'A kind of ticket (席種) a Series sells, such as S席 or オールスタンディング: a name and the price of one ticket. Neither changes once it is created; a different price is a different seat type.';
COMMENT ON COLUMN "seat_types"."id" IS 'Unique seat type identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN "seat_types"."series_id" IS 'The Series that sells the seat type';
COMMENT ON COLUMN "seat_types"."name" IS 'Name shown to fans and Organizers (1-50 characters), e.g. S席; unique within the Series';
COMMENT ON COLUMN "seat_types"."price" IS 'Price of one ticket in yen, tax-inclusive (税込): the face value of its tickets, 1 to 1,000,000';
COMMENT ON TABLE "ticket_stocks" IS 'The tickets of one seat type for one event, shared by every sale of the event. Its event, seat type and quantity never change. The sold count (Paid and Refunded orders, and Pending orders whose checkout is Committed or whose lottery entry is Won) and the held count (Pending orders whose checkout is holding) are aggregated from orders; every path that changes them first locks this row (SELECT ... FOR UPDATE).';
COMMENT ON COLUMN "ticket_stocks"."id" IS 'Unique ticket stock identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN "ticket_stocks"."event_id" IS 'The event the tickets admit to; an event of the seat type''s Series (checked by the application)';
COMMENT ON COLUMN "ticket_stocks"."seat_type_id" IS 'The seat type of the tickets; a seat type cannot be deleted while a stock uses it (NO ACTION rather than RESTRICT, so deleting a Series removes its stocks through its events and its seat types in one statement)';
COMMENT ON COLUMN "ticket_stocks"."quantity" IS 'Tickets in the stock, at least 1; never changes';
COMMENT ON INDEX "idx_ticket_stocks_seat_type_id" IS 'Optimizes the foreign key check of a seat type delete';
COMMENT ON TABLE "orders" IS 'One fan''s application for tickets of one ticket stock and, once paid, its purchase record (status 1=Paid, 2=Refunded, 4=Pending, 5=Void). Created Pending by the lottery entry or checkout that names it; Paid when its payment is captured and its tickets issued; Void when it will never be paid. One order covers all tickets of its application.';
COMMENT ON COLUMN "orders"."user_id" IS 'The fan who applied for the tickets (no FK to survive user lifecycle independently)';
COMMENT ON COLUMN "orders"."provider_payment_id" IS 'The payment provider''s ID for the captured payment (e.g. Stripe pi_...); set exactly on a Paid or Refunded order';
COMMENT ON COLUMN "orders"."status" IS 'Order status: 1=Paid, 2=Refunded, 4=Pending (applied, not yet paid), 5=Void (never paid). 3 was Failed and is unused';
COMMENT ON COLUMN "orders"."amount" IS 'Total to pay in the currency smallest unit (yen total for JPY): the seat type price times ticket_count, fixed at creation';
COMMENT ON COLUMN "orders"."pay_at" IS 'When the payment was captured and the order became Paid; set exactly on a Paid or Refunded order';
COMMENT ON COLUMN "orders"."ticket_stock_id" IS 'The ticket stock the tickets are of, which gives their event and seat type';
COMMENT ON COLUMN "orders"."ticket_count" IS 'Tickets applied for, a companion group: 1 to the per-account limit of the stock''s event';
COMMENT ON COLUMN "orders"."referral_code" IS 'The referral code of the link the fan came from, from its ?ref=: 1 to 32 lowercase ASCII letters, digits or hyphens. NULL without a code; set at creation and never changed';
COMMENT ON COLUMN "orders"."card_authorization_id" IS 'The payment provider''s ID for the card authorization (Stripe pi_...) on the fan''s card for the amount; NULL until the fan authorizes, then set once; unique';
COMMENT ON INDEX "idx_orders_user_id" IS 'Optimizes listing a user''s orders and the tickets a user holds for an event';
COMMENT ON INDEX "idx_orders_ticket_stock_status" IS 'Optimizes the sold and held counts of a ticket stock and the delete guard of a stock';
COMMENT ON INDEX "uq_orders_card_authorization_id" IS 'At most one order per card authorization; the lookup of a lottery entry or checkout by its card authorization';
COMMENT ON TABLE "ticket_sales" IS 'One sale opportunity (受付) over the events of one Series: a name, an allocation method and a window. An Organizer''s sale (discovered_at NULL) also has a verification requirement and names its events in ticket_sale_events; it sells every ticket stock of those events. A discovered sale (discovered_at set) belongs to a Series without an Organizer, names no event and is only reported to fans. A closed Organizer''s lottery is drawn once none of its lottery entries is Entered.';
COMMENT ON COLUMN "ticket_sales"."sale_end_at" IS 'When the sale closes; after sale_start_at. On an Organizer''s sale 1 to 14 days after the start for a lottery, and no later than the start of every event the sale covers (checked by the application)';
COMMENT ON COLUMN "ticket_sales"."name" IS 'Sale name shown to fans and Organizers (1-100 characters), e.g. ファンクラブ先行 or 一般発売; a discovered sale takes the name its page gives it';
COMMENT ON TABLE "checkouts" IS 'How one fan''s order is bought first come: during a first-come sale it holds the order''s tickets of its ticket stock for 15 minutes, then is committed, charged and completed, or expires or is released. Status 1=Held, 2=Committed, 3=Completed, 4=Expired, 5=Released.';
COMMENT ON COLUMN "checkouts"."card_authorization_release_at" IS 'When the order''s card authorization was released without a charge; NULL unless released';
COMMENT ON COLUMN "checkouts"."capture_at" IS 'When the order''s card authorization was captured; set once, and a row with it is never Expired or Released';
COMMENT ON COLUMN "checkouts"."order_id" IS 'The order the checkout buys: user, ticket stock, ticket count, amount, referral code and card authorization; unique';
COMMENT ON COLUMN "checkouts"."ticket_sale_id" IS 'The first-come sale the checkout buys from; a sale that names the event of the order''s ticket stock (checked by the application)';
COMMENT ON INDEX "idx_checkouts_ticket_sale_id" IS 'Optimizes the ON DELETE RESTRICT check of a sale delete';
COMMENT ON INDEX "uq_checkouts_order_id" IS 'One checkout per order';
COMMENT ON TABLE "lottery_entries" IS 'How one fan''s order takes part in an Organizer''s lottery sale, made Entered when the fan confirms an authorized card authorization; one per order and at most one per (sale, event of the order''s stock, user), checked by the application under the users row lock. State: 1=Entered, 2=Won, 3=Lost.';
COMMENT ON COLUMN "lottery_entries"."state" IS 'Lifecycle state: 1=Entered (card authorization in place), 2=Won (allocated), 3=Lost (not allocated; the order is Void)';
COMMENT ON COLUMN "lottery_entries"."draw_position" IS 'Zero-based position in the draw shuffle of the sale''s entries for the order''s ticket stock. NULL until the draw runs; the draw order of the losers for official resale.';
COMMENT ON COLUMN "lottery_entries"."order_id" IS 'The order the entry applies for: user, ticket stock, ticket count, referral code and card authorization; unique';
COMMENT ON COLUMN "lottery_entries"."ticket_sale_id" IS 'The lottery sale entered; a sale that names the event of the order''s ticket stock (checked by the application)';
COMMENT ON INDEX "idx_lottery_entries_ticket_sale_state" IS 'Optimizes listing a sale''s entries (draw batch load, stats, whether a closed lottery is drawn)';
COMMENT ON INDEX "uq_lottery_entries_order_id" IS 'One lottery entry per order';
COMMENT ON COLUMN "resale_listings"."price" IS 'Face value in yen, tax-inclusive (税込): the price of the ticket stock''s seat type';
COMMENT ON COLUMN "resale_listings"."created_at" IS 'When the fan listed the ticket';
COMMENT ON COLUMN "resale_listings"."ticket_stock_id" IS 'The ticket stock the ticket is of (ticket → order → ticket stock), resolved when listed';
COMMENT ON INDEX "idx_resale_listings_listed_by_ticket_stock" IS 'Optimizes listing the open listings of a ticket stock, oldest first';
COMMENT ON TABLE "ticket_sale_events" IS 'The events an Organizer''s ticket sale sells: at least one per Organizer''s sale, each an event of the sale''s Series (checked by the application). A discovered sale has none.';
COMMENT ON COLUMN "ticket_sale_events"."ticket_sale_id" IS 'The Organizer''s sale';
COMMENT ON COLUMN "ticket_sale_events"."event_id" IS 'An event the sale sells; every ticket stock of it is sold by the sale';
COMMENT ON INDEX "idx_ticket_sale_events_event_id" IS 'Optimizes listing the sales of an event';
COMMENT ON INDEX "idx_checkouts_due" IS 'Optimizes ListDue: lapsed holds, card authorizations to release and stalled commits';
