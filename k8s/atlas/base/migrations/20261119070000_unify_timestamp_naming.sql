-- unify-timestamp-naming: every timestamp column is named by the
-- present-tense verb of the moment it records followed by _time, as in proto
-- (created_at -> create_time, discovered_at -> find_time, sold_at -> sell_time).
-- Renames only: PostgreSQL keeps the data, indexes, constraints, partial-index
-- predicates and column comments across RENAME COLUMN, so nothing is dropped
-- and the rollback is the reverse rename. The backend release that reads the
-- new names ships in the same window (accepted short outage).
SET search_path TO app, public;

ALTER TABLE "users" RENAME COLUMN "last_login_at" TO "last_login_time";
ALTER TABLE "artists" RENAME COLUMN "fanart_synced_at" TO "fanart_sync_time";
ALTER TABLE "artists" RENAME COLUMN "official_site_checked_at" TO "official_site_check_time";
ALTER TABLE "events" RENAME COLUMN "start_at" TO "start_time";
ALTER TABLE "events" RENAME COLUMN "open_at" TO "open_time";
ALTER TABLE "concert_searches" RENAME COLUMN "searched_at" TO "search_time";
ALTER TABLE "concert_searches" RENAME COLUMN "last_found_at" TO "last_find_time";
ALTER TABLE "notifications" RENAME COLUMN "created_at" TO "create_time";
ALTER TABLE "notifications" RENAME COLUMN "read_at" TO "read_time";
ALTER TABLE "concert_candidates" RENAME COLUMN "start_at" TO "start_time";
ALTER TABLE "concert_candidates" RENAME COLUMN "open_at" TO "open_time";
ALTER TABLE "concert_candidates" RENAME COLUMN "discovered_at" TO "find_time";
ALTER TABLE "rejected_concerts" RENAME COLUMN "start_at" TO "start_time";
ALTER TABLE "rejected_concerts" RENAME COLUMN "open_at" TO "open_time";
ALTER TABLE "rejected_concerts" RENAME COLUMN "rejected_at" TO "reject_time";
ALTER TABLE "suppressed_concerts" RENAME COLUMN "start_at" TO "start_time";
ALTER TABLE "suppressed_concerts" RENAME COLUMN "suppressed_at" TO "suppress_time";
ALTER TABLE "identity_verifications" RENAME COLUMN "verify_at" TO "verify_time";
ALTER TABLE "ticket_sales" RENAME COLUMN "sale_start_at" TO "sale_start_time";
ALTER TABLE "ticket_sales" RENAME COLUMN "sale_end_at" TO "sale_end_time";
ALTER TABLE "ticket_sales" RENAME COLUMN "discovered_at" TO "find_time";
ALTER TABLE "ticket_sale_reminders" RENAME COLUMN "sent_at" TO "send_time";
ALTER TABLE "ticket_sale_searches" RENAME COLUMN "searched_at" TO "search_time";
ALTER TABLE "card_authorizations" RENAME COLUMN "expire_at" TO "expire_time";
ALTER TABLE "orders" RENAME COLUMN "pay_at" TO "pay_time";
ALTER TABLE "orders" RENAME COLUMN "confirmation_send_at" TO "confirmation_send_time";
ALTER TABLE "order_refunds" RENAME COLUMN "refund_at" TO "refund_time";
ALTER TABLE "lottery_entries" RENAME COLUMN "loss_email_send_at" TO "loss_email_send_time";
ALTER TABLE "resale_entries" RENAME COLUMN "created_at" TO "create_time";
ALTER TABLE "resale_entries" RENAME COLUMN "draw_at" TO "draw_time";
ALTER TABLE "resale_entries" RENAME COLUMN "withdraw_at" TO "withdraw_time";
ALTER TABLE "resale_entries" RENAME COLUMN "ended_email_send_at" TO "ended_email_send_time";
ALTER TABLE "checkouts" RENAME COLUMN "hold_expire_at" TO "hold_expire_time";
ALTER TABLE "checkouts" RENAME COLUMN "commit_at" TO "commit_time";
ALTER TABLE "checkouts" RENAME COLUMN "capture_at" TO "capture_time";
ALTER TABLE "tickets" RENAME COLUMN "issue_at" TO "issue_time";
ALTER TABLE "tickets" RENAME COLUMN "admit_at" TO "admit_time";
ALTER TABLE "resale_listings" RENAME COLUMN "created_at" TO "create_time";
ALTER TABLE "resale_listings" RENAME COLUMN "sold_at" TO "sell_time";
ALTER TABLE "resale_listings" RENAME COLUMN "withdraw_at" TO "withdraw_time";
ALTER TABLE "resale_listings" RENAME COLUMN "refund_at" TO "refund_time";
ALTER TABLE "resale_listings" RENAME COLUMN "sold_email_send_at" TO "sold_email_send_time";
ALTER TABLE "payout_accounts" RENAME COLUMN "provisioned_at" TO "provision_time";
ALTER TABLE "payout_accounts" RENAME COLUMN "status_synced_at" TO "status_sync_time";
ALTER TABLE "settlements" RENAME COLUMN "release_at" TO "release_time";
ALTER TABLE "settlements" RENAME COLUMN "created_at" TO "create_time";
ALTER TABLE "processed_webhook_events" RENAME COLUMN "processed_at" TO "process_time";
ALTER TABLE "wallet_public_keys" RENAME COLUMN "registered_at" TO "register_time";
ALTER TABLE "scanners" RENAME COLUMN "bound_at" TO "bind_time";
ALTER TABLE "scanners" RENAME COLUMN "revoked_at" TO "revoke_time";
ALTER TABLE "admissions" RENAME COLUMN "admitted_at" TO "admit_time";
ALTER TABLE "rejected_scans" RENAME COLUMN "scanned_at" TO "scan_time";
ALTER TABLE "passkeys" RENAME COLUMN "created_at" TO "create_time";
ALTER TABLE "watermill_events_outbox" RENAME COLUMN "created_at" TO "create_time";

ALTER INDEX "idx_concert_candidates_discovered_at" RENAME TO "idx_concert_candidates_find_time";
ALTER INDEX "idx_rejected_concerts_rejected_at" RENAME TO "idx_rejected_concerts_reject_time";

-- Comments that quote a renamed column, rewritten to the new name.
-- Set comment to table: "admissions"
COMMENT ON TABLE "admissions" IS 'Append-only attendance evidence: a ticket let in at the venue, through which scanner and when. Written together with tickets.admit_time; never changed or removed, even when the ticket is later voided.';
-- Set comment to column: "admit_time" on table: "admissions"
COMMENT ON COLUMN "admissions"."admit_time" IS 'When the ticket was let in; equal to tickets.admit_time';
-- Set comment to column: "hold_expire_time" on table: "checkouts"
COMMENT ON COLUMN "checkouts"."hold_expire_time" IS 'When the hold lapses: 15 minutes after the checkout started, never extended; a commit needs hold_expire_time > now';
-- Set comment to index: "uq_events_natural_key" on table: "events"
COMMENT ON INDEX "uq_events_natural_key" IS 'Physical identity of a performance: one row per (venue, local date, start time) among events that are not DRAFT, independent of series or performing artist. start_time is part of the key so two shows at one venue on one date with different start times (matinee/evening) are distinct; NULLS NOT DISTINCT collapses two shows whose start time is not yet published. The same physical show discovered via different artists/series resolves to one row. DRAFT events are excluded, so any number of drafts and one live or cancelled event can share a slot. Upserts infer it with ON CONFLICT (venue_id, local_event_date, start_time) WHERE publish_state IS DISTINCT FROM ''DRAFT''.';
-- Set comment to column: "provider_refund_id" on table: "resale_listings"
COMMENT ON COLUMN "resale_listings"."provider_refund_id" IS 'The payment provider''s ID for the refund of the refund amount to the listing''s user (e.g. Stripe "re_..."); set once together with refund_time';
-- Set comment to column: "event_id" on table: "settlements"
COMMENT ON COLUMN "settlements"."event_id" IS 'The event this settlement gates on. Stored denormalized so the release-gate check reads events.start_time without extra joins.';
-- Set comment to table: "ticket_sales"
COMMENT ON TABLE "ticket_sales" IS 'One sale opportunity (受付) over the events of one Series: a name, an allocation method and a window. An Organizer''s sale (find_time NULL) also has a verification requirement and names its events in ticket_sale_events; it sells every ticket stock of those events. A discovered sale (find_time set) belongs to a Series without an Organizer, names no event and is only reported to fans. A closed Organizer''s lottery is drawn once none of its lottery entries is Entered.';
-- Set comment to column: "sale_end_time" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."sale_end_time" IS 'When the sale closes; after sale_start_time. On an Organizer''s sale 1 to 14 days after the start for a lottery, and no later than the start of every event the sale covers (checked by the application)';
-- Set comment to column: "find_time" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."find_time" IS 'When discovery first stored the sale; set on insert and never updated. NULL exactly on an Organizer''s sale. The reminder scan uses it as the first-sight guard: a stage whose anchor is before find_time is not sent.';
-- Set comment to column: "sale_start_date_jst" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."sale_start_date_jst" IS 'Calendar date of sale_start_time in Asia/Tokyo; part of the discovered-sale convergence key';
-- Set comment to column: "result_date" on table: "ticket_sales"
COMMENT ON COLUMN "ticket_sales"."result_date" IS 'Calendar day in Japan time (Asia/Tokyo) on which a discovered lottery''s results are announced, as its pages state it; no earlier than the Japan-time day of sale_end_time. NULL when the pages do not state it, and always NULL on a first-come sale and on an Organizer''s sale. Anchors the RESULT_DAY reminder.';
-- Set comment to index: "uq_ticket_sales_discovered_convergence" on table: "ticket_sales"
COMMENT ON INDEX "uq_ticket_sales_discovered_convergence" IS 'One discovered sale per (series, method, start date in Japan time): re-discovery updates it in place. Partial, so an Organizer may open two sales of one Series on the same day for different events. Upserts infer it with ON CONFLICT (series_id, method, sale_start_date_jst) WHERE find_time IS NOT NULL.';
