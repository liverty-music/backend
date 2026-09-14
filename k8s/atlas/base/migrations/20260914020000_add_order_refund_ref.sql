-- Add refund_ref column to orders for durable audit of the Stripe Refund
-- reference (re_...) issued on CANCELLATION / POSTPONEMENT_WINDOW refunds.
-- Empty string for DISPUTE reason (no Stripe Refund object is created — the
-- chargeback reversal is handled at the card-network level) and for orders
-- that have not yet been refunded. Part of settlement task 4.1.

SET search_path TO app, public;

ALTER TABLE orders ADD COLUMN IF NOT EXISTS refund_ref TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN orders.refund_ref IS 'Opaque provider Refund reference (e.g. Stripe "re_...") set when the order is refunded via CANCELLATION or POSTPONEMENT_WINDOW. Empty for DISPUTE reason (the chargeback already reversed the charge at the card network) and for orders that are not yet refunded.';
