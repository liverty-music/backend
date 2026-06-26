-- Create "notifications" table
CREATE TABLE "notifications" (
  "id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "type" text NOT NULL,
  "payload" jsonb NOT NULL,
  "delivery_status" text NOT NULL DEFAULT 'queued',
  "failure_reason" text NULL,
  "queued_at" timestamptz NOT NULL DEFAULT now(),
  "delivered_at" timestamptz NULL,
  "read_at" timestamptz NULL,
  "dismissed_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_notifications_delivery_status" CHECK (delivery_status = ANY (ARRAY['queued'::text, 'delivered'::text, 'failed'::text])),
  CONSTRAINT "chk_notifications_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "notifications_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Set comment to table: "notifications"
COMMENT ON TABLE "notifications" IS 'Notification log: one durable record per user-facing notification, with per-channel delivery state (queued/delivered/failed) and per-user read/dismiss state. Source of truth for delivery auditing and the in-app inbox.';
-- Set comment to column: "id" on table: "notifications"
COMMENT ON COLUMN "notifications"."id" IS 'Unique notification identifier (UUIDv7, application-generated). Propagated into the push payload data.notification_id as the end-to-end correlation key.';
-- Set comment to column: "user_id" on table: "notifications"
COMMENT ON COLUMN "notifications"."user_id" IS 'Reference to the recipient user';
-- Set comment to column: "type" on table: "notifications"
COMMENT ON COLUMN "notifications"."type" IS 'Notification type: new_concerts, sales_reminder, sales_phase_announcement';
-- Set comment to column: "payload" on table: "notifications"
COMMENT ON COLUMN "notifications"."payload" IS 'Rendered notification payload (title, body, url, tag) as delivered to the channel';
-- Set comment to column: "delivery_status" on table: "notifications"
COMMENT ON COLUMN "notifications"."delivery_status" IS 'Web-push channel delivery state: queued (on creation), delivered (push service accepted the send), or failed';
-- Set comment to column: "failure_reason" on table: "notifications"
COMMENT ON COLUMN "notifications"."failure_reason" IS 'Human-readable reason set when delivery_status is failed; NULL otherwise';
-- Set comment to column: "queued_at" on table: "notifications"
COMMENT ON COLUMN "notifications"."queued_at" IS 'Timestamp when the notification record was created in the queued state';
-- Set comment to column: "delivered_at" on table: "notifications"
COMMENT ON COLUMN "notifications"."delivered_at" IS 'Timestamp when the channel accepted the send; NULL until delivered';
-- Set comment to column: "read_at" on table: "notifications"
COMMENT ON COLUMN "notifications"."read_at" IS 'Timestamp when the user marked the notification read; NULL until read';
-- Set comment to column: "dismissed_at" on table: "notifications"
COMMENT ON COLUMN "notifications"."dismissed_at" IS 'Timestamp when the user dismissed the notification; NULL until dismissed';
-- Create index "idx_notifications_user_queued" to table: "notifications"
CREATE INDEX "idx_notifications_user_queued" ON "notifications" ("user_id", "queued_at" DESC);
-- Set comment to index: "idx_notifications_user_queued"
COMMENT ON INDEX "idx_notifications_user_queued" IS 'Optimizes the inbox query: a user''s notifications most-recent-first';
