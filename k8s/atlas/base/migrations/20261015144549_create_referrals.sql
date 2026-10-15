-- Create referrals (referral-attribution, design D1 and D6): the referral code a
-- checkout or a lottery entry was started with. No grant is written: the
-- read-write roles' default privileges give fan-api full CRUD, the SELECT default
-- privilege of organizer-console-api covers the new table, and reception-api
-- gets nothing.

SET search_path TO app, public;

-- Create "referrals" table
CREATE TABLE "referrals" (
  "id" uuid NOT NULL,
  "checkout_id" uuid NULL,
  "lottery_entry_id" uuid NULL,
  "code" text NOT NULL,
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "referrals_checkout_id_fkey" FOREIGN KEY ("checkout_id") REFERENCES "checkouts" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "referrals_lottery_entry_id_fkey" FOREIGN KEY ("lottery_entry_id") REFERENCES "lottery_entries" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "chk_referrals_code" CHECK (code ~ '^[a-z0-9-]{1,32}$'::text),
  CONSTRAINT "chk_referrals_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_referrals_one_source" CHECK ((checkout_id IS NULL) <> (lottery_entry_id IS NULL))
);
-- Create index "uq_referrals_checkout_id" to table: "referrals"
CREATE UNIQUE INDEX "uq_referrals_checkout_id" ON "referrals" ("checkout_id") WHERE (checkout_id IS NOT NULL);
-- Create index "uq_referrals_lottery_entry_id" to table: "referrals"
CREATE UNIQUE INDEX "uq_referrals_lottery_entry_id" ON "referrals" ("lottery_entry_id") WHERE (lottery_entry_id IS NOT NULL);
-- Set comment to table: "referrals"
COMMENT ON TABLE "referrals" IS 'The referral code one checkout or one lottery entry was started with, from the ?ref= of the link the fan followed. At most one row per source, removed with its source; a purchase without a code has no row.';
-- Set comment to column: "id" on table: "referrals"
COMMENT ON COLUMN "referrals"."id" IS 'Unique referral identifier (UUIDv7, application-generated)';
-- Set comment to column: "checkout_id" on table: "referrals"
COMMENT ON COLUMN "referrals"."checkout_id" IS 'The checkout started with the code; NULL when the source is a lottery entry; unique';
-- Set comment to column: "lottery_entry_id" on table: "referrals"
COMMENT ON COLUMN "referrals"."lottery_entry_id" IS 'The lottery entry started with the code; NULL when the source is a checkout; unique';
-- Set comment to column: "code" on table: "referrals"
COMMENT ON COLUMN "referrals"."code" IS 'The referral code: 1 to 32 lowercase ASCII letters, digits or hyphens. Replaced only while the source is a Held checkout or a Draft entry';
-- Set comment to column: "created_at" on table: "referrals"
COMMENT ON COLUMN "referrals"."created_at" IS 'When the source was first started with a code; kept when the code is replaced';
-- Set comment to index: "uq_referrals_checkout_id" on table: "referrals"
COMMENT ON INDEX "uq_referrals_checkout_id" IS 'At most one referral per checkout; the conflict target of Record and the join from orders.checkout_id';
-- Set comment to index: "uq_referrals_lottery_entry_id" on table: "referrals"
COMMENT ON INDEX "uq_referrals_lottery_entry_id" IS 'At most one referral per lottery entry; the conflict target of Record and the join from orders.lottery_entry_id';
