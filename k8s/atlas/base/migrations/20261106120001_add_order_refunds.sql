-- Move the refund record of an Order out of orders.provider_refund_id into
-- order_refunds, with the reason, the amount and the time
-- (notify-fans-of-cancellation-and-refund, design D1).
--
-- No Order was Refunded in production when this was written (counted
-- 2026-11-06), and the reason and time of an earlier refund are stored
-- nowhere, so nothing is copied. The guard below stops the migration instead
-- of dropping the provider refund ID of a Refunded Order without a record.
SET search_path TO app, public;

-- Set comment to column: "type" on table: "notifications"
COMMENT ON COLUMN "notifications"."type" IS 'Notification type: new_concerts, sales_reminder, ticket_sale_announcement, order_confirmation, lottery_loss, resale_entry_lapsed, resale_entry_lost, event_cancelled, order_refunded';
-- Create "order_refunds" table
CREATE TABLE "order_refunds" (
  "order_id" uuid NOT NULL,
  "reason" smallint NOT NULL,
  "amount" bigint NOT NULL,
  "provider_refund_id" text NULL,
  "refund_at" timestamptz NOT NULL,
  PRIMARY KEY ("order_id"),
  CONSTRAINT "order_refunds_order_id_fkey" FOREIGN KEY ("order_id") REFERENCES "orders" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "chk_order_refunds_amount" CHECK (amount >= 0),
  CONSTRAINT "chk_order_refunds_provider_refund_id_not_empty" CHECK ((provider_refund_id IS NULL) OR (provider_refund_id <> ''::text)),
  CONSTRAINT "chk_order_refunds_reason" CHECK (reason = ANY (ARRAY[1, 3]))
);
-- Set comment to table: "order_refunds"
COMMENT ON TABLE "order_refunds" IS 'The refund of a Refunded order: why, how much and when. A row exists exactly for an order with status 2, written with that status by Order.CommitRefund and removed with the order';
-- Set comment to column: "order_id" on table: "order_refunds"
COMMENT ON COLUMN "order_refunds"."order_id" IS 'The Refunded order';
-- Set comment to column: "reason" on table: "order_refunds"
COMMENT ON COLUMN "order_refunds"."reason" IS 'Refund reason: 1=Cancellation (the event was cancelled, 中止), 3=Dispute (the card issuer returned the money). 2 is retired';
-- Set comment to column: "amount" on table: "order_refunds"
COMMENT ON COLUMN "order_refunds"."amount" IS 'Amount refunded to the fan''s card by the platform, in the order currency smallest unit: the price of the tickets still issued for a cancellation; 0 for a dispute or when every ticket was resold';
-- Set comment to column: "provider_refund_id" on table: "order_refunds"
COMMENT ON COLUMN "order_refunds"."provider_refund_id" IS 'The payment provider''s ID for the refund (e.g. Stripe "re_..."); NULL when the platform moved no money (a dispute, or every ticket resold)';
-- Set comment to column: "refund_at" on table: "order_refunds"
COMMENT ON COLUMN "order_refunds"."refund_at" IS 'When the order was refunded';

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM orders WHERE status = 2) THEN
    RAISE EXCEPTION 'Refunded orders exist; decide their order_refunds backfill before dropping orders.provider_refund_id';
  END IF;
END
$$;
-- Modify "orders" table
ALTER TABLE "orders" DROP COLUMN "provider_refund_id";
