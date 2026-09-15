-- Add rescheduled_at column to events: marks the moment the organizer announced
-- this event was rescheduled (延期) and starts the holder-initiated postponement
-- refund window. Server-owned; NULL until an organizer reschedule flow stamps it.
-- Part of ticket-purchase-and-issuance task 4.2.

SET search_path TO app, public;

ALTER TABLE events ADD COLUMN IF NOT EXISTS rescheduled_at TIMESTAMPTZ;

COMMENT ON COLUMN events.rescheduled_at IS 'Timestamp when the organizer announced this event was rescheduled (延期). Server-owned; set by the organizer reschedule flow. NULL when the event has never been postponed. Marks the start of the holder-initiated postponement refund window (PostponementRefundWindow). Until an organizer reschedule flow stamps this column, the refund gate falls back to admin-authoritative.';
