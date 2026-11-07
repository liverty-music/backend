-- A Notification stores what it is about, not its rendered message or its
-- delivery outcome, and a User always has a preferred language
-- (add-notification-center, design "Migration Plan").
--
-- Every notifications row is deleted: the rows hold rendered messages that
-- cannot be turned into subjects, and production holds only test data. The
-- reminder sent-log loses the result-day stage, which is no longer sent. No user
-- in production lacks a preferred language (counted when the change was planned);
-- SET NOT NULL stops the migration if one does.
SET search_path TO app, public;

DELETE FROM notifications;

ALTER TABLE notifications
    DROP CONSTRAINT chk_notifications_delivery_status,
    DROP COLUMN payload,
    DROP COLUMN delivery_status,
    DROP COLUMN failure_reason,
    DROP COLUMN delivered_at,
    DROP COLUMN dismissed_at,
    ADD COLUMN subject JSONB NOT NULL;

ALTER TABLE notifications RENAME COLUMN queued_at TO created_at;
ALTER INDEX idx_notifications_user_queued RENAME TO idx_notifications_user_created;

COMMENT ON TABLE notifications IS 'Notification log: one durable record per user-facing notification, with what it is about and whether the fan has read it. Its message is rendered from the subject each time.';
COMMENT ON COLUMN notifications.type IS 'Notification type: new_concerts, sales_reminder, ticket_sale_announcement, lottery_win, resale_win, lottery_loss, resale_entry_lapsed, resale_entry_lost, concert_cancelled, order_refunded';
COMMENT ON COLUMN notifications.subject IS 'The ids of the records the notification is about (artist_id, event_ids, event_id, ticket_sale_id, stage, order_id, lottery_entry_id, resale_entry_id), as its type defines';
COMMENT ON COLUMN notifications.created_at IS 'Timestamp when the notification record was created';
COMMENT ON INDEX idx_notifications_user_created IS 'Optimizes the notification center query: a user''s notifications newest first';

ALTER TABLE users ALTER COLUMN preferred_language SET NOT NULL;
COMMENT ON COLUMN users.preferred_language IS 'User preferred language code (e.g., en, ja). Set when the user is created.';

DELETE FROM ticket_sale_reminders WHERE stage = 4;
ALTER TABLE ticket_sale_reminders
    DROP CONSTRAINT chk_ticket_sale_reminders_stage,
    ADD CONSTRAINT chk_ticket_sale_reminders_stage CHECK (stage IN (1, 2));
COMMENT ON COLUMN ticket_sale_reminders.stage IS 'Reminder stage: 1=APPLY_OPEN (at the start for a lottery, 30 minutes before it for a first-come sale), 2=APPLY_CLOSE_24H (24h before the end, lottery only). 3 (APPLY_CLOSE_1H) and 4 (RESULT_DAY) are not used.';
