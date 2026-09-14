-- Add processed_webhook_events table for Stripe webhook idempotency.
-- Each provider event id is recorded after the event is applied so that
-- duplicate Stripe deliveries are detected and skipped without re-applying
-- the side effect (no double-refund, no double-reversal).
-- Part of settlement task 4.3.

SET search_path TO app, public;

CREATE TABLE IF NOT EXISTS processed_webhook_events (
    provider_event_id TEXT        PRIMARY KEY,
    processed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE processed_webhook_events IS 'Idempotency guard for Stripe webhook events. Each provider event id is recorded after the event is applied; a duplicate delivery is detected via EXISTS check and skipped. Covers charge.refunded, charge.dispute.created, transfer.reversed, payout.paid, and payout.failed event types.';
COMMENT ON COLUMN processed_webhook_events.provider_event_id IS 'Stripe event id (e.g. evt_...). Primary key — unique per event.';
COMMENT ON COLUMN processed_webhook_events.processed_at IS 'Timestamp when this event was first applied.';
