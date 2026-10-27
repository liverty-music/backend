-- enter-lottery-at-confirm (design "Schema migration"): a lottery entry is made
-- Entered at Confirm with its card authorization, so the Draft (5) and
-- Withdrawn (4) states go, and the Referral entity is folded into a
-- referral_code column of checkouts and lottery_entries. The referral codes
-- are copied before the Draft and Withdrawn rows are deleted and before
-- referrals is dropped. A read-only prod check on 2026-10-27 found no lottery
-- entries at all. No grant is written: the new columns are covered by the
-- table-level privileges the roles already hold, and the dropped table takes
-- its grants with it.

SET search_path TO app, public;

-- 1. The referral code moves onto its Checkout or LotteryEntry.
ALTER TABLE "checkouts" ADD COLUMN "referral_code" text NULL, ADD CONSTRAINT "chk_checkouts_referral_code" CHECK (referral_code ~ '^[a-z0-9-]{1,32}$'::text);
ALTER TABLE "lottery_entries" ADD COLUMN "referral_code" text NULL, ADD CONSTRAINT "chk_lottery_entries_referral_code" CHECK (referral_code ~ '^[a-z0-9-]{1,32}$'::text);
UPDATE "checkouts" AS c SET "referral_code" = r."code" FROM "referrals" AS r WHERE r."checkout_id" = c."id";
UPDATE "lottery_entries" AS le SET "referral_code" = r."code" FROM "referrals" AS r WHERE r."lottery_entry_id" = le."id";
DROP TABLE "referrals";

-- 2. Draft and Withdrawn entries are deleted (prod holds none).
DELETE FROM "lottery_entries" WHERE "state" IN (4, 5);

-- 3. Every entry is Entered, Won or Lost and holds its own card authorization.
DROP INDEX "uq_lottery_entries_active";
DROP INDEX "idx_lottery_entries_ticket_type_id";
ALTER TABLE "lottery_entries" DROP CONSTRAINT "chk_lottery_entries_authorized_from_entered", DROP CONSTRAINT "chk_lottery_entries_card_authorization_id_not_empty", ADD CONSTRAINT "chk_lottery_entries_card_authorization_id_not_empty" CHECK (card_authorization_id <> ''::text), DROP CONSTRAINT "chk_lottery_entries_state", ADD CONSTRAINT "chk_lottery_entries_state" CHECK (state = ANY (ARRAY[1, 2, 3])), ALTER COLUMN "card_authorization_id" SET NOT NULL;
CREATE UNIQUE INDEX "uq_lottery_entries_ticket_type_user" ON "lottery_entries" ("ticket_type_id", "user_id");
CREATE UNIQUE INDEX "uq_lottery_entries_card_authorization_id" ON "lottery_entries" ("card_authorization_id");

-- 4. Comments.
COMMENT ON COLUMN "checkouts"."referral_code" IS 'The referral code of the last link the fan followed before paying, from its ?ref=: 1 to 32 lowercase ASCII letters, digits or hyphens. NULL without a code; replaced only while Held';
COMMENT ON TABLE "lottery_entries" IS 'Fan entries into the lottery of a ticket type, made Entered when the fan confirms an authorized card authorization; at most one per (ticket type, user). State: 1=Entered, 2=Won, 3=Lost.';
COMMENT ON COLUMN "lottery_entries"."card_authorization_id" IS 'The payment provider''s ID for the card authorization (Stripe pi_...) the entry was made from; set at creation, unique';
COMMENT ON COLUMN "lottery_entries"."state" IS 'Lifecycle state: 1=Entered (card authorization in place), 2=Won (captured), 3=Lost (card authorization released)';
COMMENT ON COLUMN "lottery_entries"."referral_code" IS 'The referral code the card authorization was opened with, from the ?ref= of the link the fan followed: 1 to 32 lowercase ASCII letters, digits or hyphens. NULL without a code; set at creation and never changed';
COMMENT ON INDEX "uq_lottery_entries_ticket_type_user" IS 'At most one entry per (ticket type, user), in any state; also serves listing a ticket type''s entries (draw batch load, stats)';
COMMENT ON INDEX "uq_lottery_entries_card_authorization_id" IS 'At most one entry per card authorization; the lookup of GetByCardAuthorizationID';
