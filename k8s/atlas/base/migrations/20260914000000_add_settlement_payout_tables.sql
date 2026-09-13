-- Create "organizer_connected_accounts" table
CREATE TABLE "organizer_connected_accounts" (
  "organizer_id" uuid NOT NULL,
  "account_ref" text NOT NULL,
  "status" smallint NOT NULL,
  "provisioned_at" timestamptz NOT NULL DEFAULT now(),
  "status_synced_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("organizer_id"),
  CONSTRAINT "organizer_connected_accounts_organizer_id_fkey" FOREIGN KEY ("organizer_id") REFERENCES "organizers" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "chk_oca_account_ref_not_empty" CHECK (account_ref <> ''),
  CONSTRAINT "chk_oca_status" CHECK (status = ANY (ARRAY[1, 2, 3]))
);
-- Set comment to table: "organizer_connected_accounts"
COMMENT ON TABLE "organizer_connected_accounts" IS 'Opaque provider reference to the Organizer payout-recipient connected account (Accounts v2, transfers capability only). One row per Organizer. status: 1=Pending (KYC/KYB in progress), 2=Active (eligible for payout), 3=Restricted (capability disabled).';
-- Set comment to column: "organizer_id" on table: "organizer_connected_accounts"
COMMENT ON COLUMN "organizer_connected_accounts"."organizer_id" IS 'The Organizer that owns this payout-recipient account (1:1 with organizers)';
-- Set comment to column: "account_ref" on table: "organizer_connected_accounts"
COMMENT ON COLUMN "organizer_connected_accounts"."account_ref" IS 'Opaque provider connected-account reference (e.g. Stripe "acct_..."); meaningful only to the provider';
-- Set comment to column: "status" on table: "organizer_connected_accounts"
COMMENT ON COLUMN "organizer_connected_accounts"."status" IS 'Payout-onboarding readiness: 1=Pending, 2=Active, 3=Restricted. Eligibility = status 2 only.';
-- Set comment to column: "provisioned_at" on table: "organizer_connected_accounts"
COMMENT ON COLUMN "organizer_connected_accounts"."provisioned_at" IS 'When the connected account was first provisioned via the payment provider';
-- Set comment to column: "status_synced_at" on table: "organizer_connected_accounts"
COMMENT ON COLUMN "organizer_connected_accounts"."status_synced_at" IS 'When the capability status was last refreshed from the payment provider';
-- Create "settlements" table
CREATE TABLE "settlements" (
  "id" uuid NOT NULL,
  "order_id" uuid NOT NULL,
  "organizer_id" uuid NOT NULL,
  "event_id" uuid NOT NULL,
  "charge_ref" text NULL,
  "status" smallint NOT NULL,
  "released_at" timestamptz NULL,
  "settled_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "settlements_order_id_fkey" FOREIGN KEY ("order_id") REFERENCES "orders" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "settlements_event_id_fkey" FOREIGN KEY ("event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "chk_settlements_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_settlements_status" CHECK (status = ANY (ARRAY[1, 2, 3])),
  CONSTRAINT "chk_settlements_charge_ref_not_empty" CHECK (charge_ref IS NULL OR charge_ref <> '')
);
-- Set comment to table: "settlements"
COMMENT ON TABLE "settlements" IS 'Payout record for one Order. Holds the captured platform charge until the release gate passes (event start_time + dispute buffer), then tracks the Transfer(s) that pay out the Organizer net share. Platform fee = un-transferred remainder (no fee split row). status: 1=Held, 2=Released, 3=Reversed.';
-- Set comment to column: "id" on table: "settlements"
COMMENT ON COLUMN "settlements"."id" IS 'Unique settlement identifier (UUIDv7, application-generated)';
-- Set comment to column: "order_id" on table: "settlements"
COMMENT ON COLUMN "settlements"."order_id" IS 'The Order this settlement pays out; one settlement per order (unique index)';
-- Set comment to column: "organizer_id" on table: "settlements"
COMMENT ON COLUMN "settlements"."organizer_id" IS 'The Organizer that receives the payout. Stored denormalized so the sweeper avoids joining through application → phase → event.';
-- Set comment to column: "event_id" on table: "settlements"
COMMENT ON COLUMN "settlements"."event_id" IS 'The event this settlement gates on. Stored denormalized so the release-gate check reads events.start_at without extra joins.';
-- Set comment to column: "charge_ref" on table: "settlements"
COMMENT ON COLUMN "settlements"."charge_ref" IS 'Opaque provider Charge reference (e.g. Stripe "ch_...") resolved from the Order PaymentIntent at payout time. NULL until resolved. Used as source_transaction on each Transfer.';
-- Set comment to column: "status" on table: "settlements"
COMMENT ON COLUMN "settlements"."status" IS 'Settlement lifecycle: 1=Held (gate not passed), 2=Released (Transfer(s) created), 3=Reversed (transfer_reversal clawback applied)';
-- Set comment to column: "released_at" on table: "settlements"
COMMENT ON COLUMN "settlements"."released_at" IS 'When the settlement was released (Transfer(s) created). NULL while Held.';
-- Set comment to column: "settled_at" on table: "settlements"
COMMENT ON COLUMN "settlements"."settled_at" IS 'When this settlement row was created (= Order issuance time for the initial Held row)';
-- Create index "uq_settlements_order_id" to table: "settlements"
CREATE UNIQUE INDEX "uq_settlements_order_id" ON "settlements" ("order_id");
-- Set comment to index: "uq_settlements_order_id"
COMMENT ON INDEX "uq_settlements_order_id" IS 'One settlement per Order — the payout idempotency guard; a duplicate insert raises unique_violation.';
-- Create index "idx_settlements_status" to table: "settlements"
CREATE INDEX "idx_settlements_status" ON "settlements" ("status");
-- Set comment to index: "idx_settlements_status"
COMMENT ON INDEX "idx_settlements_status" IS 'Optimizes the payout sweeper''s ListHeld scan (WHERE status = 1)';
-- Create index "idx_settlements_organizer_id" to table: "settlements"
CREATE INDEX "idx_settlements_organizer_id" ON "settlements" ("organizer_id");
-- Set comment to index: "idx_settlements_organizer_id"
COMMENT ON INDEX "idx_settlements_organizer_id" IS 'Optimizes listing an Organizer''s settlements for the console';
-- Create "settlement_splits" table
CREATE TABLE "settlement_splits" (
  "settlement_id" uuid NOT NULL,
  "payee_organizer_id" uuid NOT NULL,
  "amount" bigint NOT NULL,
  "transfer_ref" text NULL,
  "transfer_reversal_ref" text NULL,
  PRIMARY KEY ("settlement_id", "payee_organizer_id"),
  CONSTRAINT "settlement_splits_settlement_id_fkey" FOREIGN KEY ("settlement_id") REFERENCES "settlements" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "chk_settlement_splits_amount_positive" CHECK (amount > 0),
  CONSTRAINT "chk_settlement_splits_transfer_ref_not_empty" CHECK (transfer_ref IS NULL OR transfer_ref <> ''),
  CONSTRAINT "chk_settlement_splits_reversal_ref_not_empty" CHECK (transfer_reversal_ref IS NULL OR transfer_reversal_ref <> '')
);
-- Set comment to table: "settlement_splits"
COMMENT ON TABLE "settlement_splits" IS 'One payee share per settlement. MVP = one row per settlement (the Organizer). Extensible to N payees (venue, artist) by adding rows. The platform fee is the un-transferred remainder and has no row here.';
-- Set comment to column: "settlement_id" on table: "settlement_splits"
COMMENT ON COLUMN "settlement_splits"."settlement_id" IS 'Reference to the parent settlement';
-- Set comment to column: "payee_organizer_id" on table: "settlement_splits"
COMMENT ON COLUMN "settlement_splits"."payee_organizer_id" IS 'Organizer that receives this split (no FK so payee lifecycle is independent of the settlement)';
-- Set comment to column: "amount" on table: "settlement_splits"
COMMENT ON COLUMN "settlement_splits"."amount" IS 'Net share in the Order currency smallest unit (whole yen for JPY). Must be positive.';
-- Set comment to column: "transfer_ref" on table: "settlement_splits"
COMMENT ON COLUMN "settlement_splits"."transfer_ref" IS 'Opaque provider Transfer reference (e.g. Stripe "tr_..."). NULL until the split is released.';
-- Set comment to column: "transfer_reversal_ref" on table: "settlement_splits"
COMMENT ON COLUMN "settlement_splits"."transfer_reversal_ref" IS 'Opaque provider transfer-reversal reference (e.g. Stripe "trr_..."). NULL unless reversed on a refund/dispute.';
