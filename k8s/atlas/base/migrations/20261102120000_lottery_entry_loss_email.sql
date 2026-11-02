-- make-lottery-draw-durable: a Lost lottery entry records when its loss email
-- was sent, so each losing fan is emailed once; the notification type
-- lottery_loss is added to the type comment. Comments also follow the new
-- meaning of Won (allocated by the draw, charged afterwards) and Lost (a win
-- whose card could not be charged is reverted).

SET search_path TO app, public;

ALTER TABLE lottery_entries ADD COLUMN loss_email_send_at TIMESTAMPTZ;
COMMENT ON COLUMN lottery_entries.loss_email_send_at IS 'When the email telling the fan the entry lost was sent; set once right after a successful send';
COMMENT ON COLUMN lottery_entries.state IS 'Lifecycle state: 1=Entered (card authorization in place), 2=Won (allocated by the draw; the order is Pending until charged, then Paid), 3=Lost (not allocated, or the win was reverted because the card could not be charged; the order is Void)';
COMMENT ON COLUMN notifications.type IS 'Notification type: new_concerts, sales_reminder, ticket_sale_announcement, order_confirmation, lottery_loss';
