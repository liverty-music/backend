-- Create "orders" table
CREATE TABLE "orders" (
  "id" uuid NOT NULL,
  "buyer_id" uuid NOT NULL,
  "application_id" uuid NOT NULL,
  "provider" smallint NOT NULL,
  "payment_intent_ref" text NOT NULL,
  "payment_method_ref" text NOT NULL DEFAULT '',
  "card_brand" text NOT NULL DEFAULT '',
  "card_last4" text NOT NULL DEFAULT '',
  "status" smallint NOT NULL,
  "amount" bigint NOT NULL,
  "currency" text NOT NULL,
  "paid_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "orders_application_id_fkey" FOREIGN KEY ("application_id") REFERENCES "ticket_applications" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "chk_orders_amount_positive" CHECK (amount > 0),
  CONSTRAINT "chk_orders_currency_len" CHECK (char_length(currency) = 3),
  CONSTRAINT "chk_orders_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_orders_provider" CHECK (provider = ANY (ARRAY[1, 2])),
  CONSTRAINT "chk_orders_status" CHECK (status = ANY (ARRAY[1, 2, 3]))
);
-- Create index "uq_orders_application_id" to table: "orders"
CREATE UNIQUE INDEX "uq_orders_application_id" ON "orders" ("application_id");
-- Create index "idx_orders_buyer_id" to table: "orders"
CREATE INDEX "idx_orders_buyer_id" ON "orders" ("buyer_id");
-- Set comment to table: "orders"
COMMENT ON TABLE "orders" IS 'Purchase record for one winning lottery application. Created already paid from the captured winning payment (status 1=Paid, 2=Refunded, 3=Failed; no pending). One order covers the N tickets of the winning application.';
-- Set comment to column: "id" on table: "orders"
COMMENT ON COLUMN "orders"."id" IS 'Unique order identifier (UUIDv7, application-generated)';
-- Set comment to column: "buyer_id" on table: "orders"
COMMENT ON COLUMN "orders"."buyer_id" IS 'The winning applicant user ID (no FK to survive user lifecycle independently)';
-- Set comment to column: "application_id" on table: "orders"
COMMENT ON COLUMN "orders"."application_id" IS 'The Won-captured application this order derives from; unique (one order per application)';
-- Set comment to column: "provider" on table: "orders"
COMMENT ON COLUMN "orders"."provider" IS 'Payment provider: 1=Stripe, 2=KOMOJU';
-- Set comment to column: "payment_intent_ref" on table: "orders"
COMMENT ON COLUMN "orders"."payment_intent_ref" IS 'Opaque provider PaymentIntent reference (e.g. Stripe pi_...) of the captured payment';
-- Set comment to column: "payment_method_ref" on table: "orders"
COMMENT ON COLUMN "orders"."payment_method_ref" IS 'Opaque provider PaymentMethod reference (e.g. Stripe pm_...), optional';
-- Set comment to column: "card_brand" on table: "orders"
COMMENT ON COLUMN "orders"."card_brand" IS 'Display-only card brand facet (e.g. visa); never the PAN';
-- Set comment to column: "card_last4" on table: "orders"
COMMENT ON COLUMN "orders"."card_last4" IS 'Display-only last four digits; never the full PAN';
-- Set comment to column: "status" on table: "orders"
COMMENT ON COLUMN "orders"."status" IS 'Order status: 1=Paid, 2=Refunded, 3=Failed (capture-succeeded-but-issuance-refunded edge)';
-- Set comment to column: "amount" on table: "orders"
COMMENT ON COLUMN "orders"."amount" IS 'Total captured amount in the currency smallest unit (yen total for JPY)';
-- Set comment to column: "currency" on table: "orders"
COMMENT ON COLUMN "orders"."currency" IS 'ISO 4217 currency code of amount (JPY for the MVP)';
-- Set comment to column: "paid_at" on table: "orders"
COMMENT ON COLUMN "orders"."paid_at" IS 'When the payment was captured (= the capture time at the draw)';
-- Create "tickets" table
CREATE TABLE "tickets" (
  "id" uuid NOT NULL,
  "order_id" uuid NOT NULL,
  "holder_id" uuid NOT NULL,
  "event_id" uuid NOT NULL,
  "holder_full_name" text NOT NULL,
  "holder_phone_number" text NOT NULL,
  "verified_identity_id" uuid NULL,
  "resale_without_consent_prohibited" boolean NOT NULL DEFAULT true,
  "status" smallint NOT NULL,
  "issued_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "tickets_event_id_fkey" FOREIGN KEY ("event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "tickets_order_id_fkey" FOREIGN KEY ("order_id") REFERENCES "orders" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "chk_tickets_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_tickets_resale_prohibited" CHECK (resale_without_consent_prohibited = true),
  CONSTRAINT "chk_tickets_status" CHECK (status = ANY (ARRAY[1, 2]))
);
-- Create index "idx_tickets_order_id" to table: "tickets"
CREATE INDEX "idx_tickets_order_id" ON "tickets" ("order_id");
-- Create index "idx_tickets_holder_id" to table: "tickets"
CREATE INDEX "idx_tickets_holder_id" ON "tickets" ("holder_id");
-- Create index "idx_tickets_event_id" to table: "tickets"
CREATE INDEX "idx_tickets_event_id" ON "tickets" ("event_id");
-- Set comment to table: "tickets"
COMMENT ON TABLE "tickets" IS 'Account-bound covered tickets (特定興行入場券) issued from a captured lottery win. Each carries the three covered-ticket conditions: resale-without-consent prohibited, date/venue+eligible-person (event_id + holder identity), and bound 本人確認.';
-- Set comment to column: "id" on table: "tickets"
COMMENT ON COLUMN "tickets"."id" IS 'Unique ticket identifier (UUIDv7, application-generated)';
-- Set comment to column: "order_id" on table: "tickets"
COMMENT ON COLUMN "tickets"."order_id" IS 'The order that issued this ticket';
-- Set comment to column: "holder_id" on table: "tickets"
COMMENT ON COLUMN "tickets"."holder_id" IS 'The account the ticket is bound to (current holder; reassigned by official resale)';
-- Set comment to column: "event_id" on table: "tickets"
COMMENT ON COLUMN "tickets"."event_id" IS 'The event this ticket admits to (supplies the covered-ticket face date/venue)';
-- Set comment to column: "holder_full_name" on table: "tickets"
COMMENT ON COLUMN "tickets"."holder_full_name" IS 'Holder legal name (本人確認) noted on the covered-ticket face';
-- Set comment to column: "holder_phone_number" on table: "tickets"
COMMENT ON COLUMN "tickets"."holder_phone_number" IS 'Holder contact phone (本人確認)';
-- Set comment to column: "verified_identity_id" on table: "tickets"
COMMENT ON COLUMN "tickets"."verified_identity_id" IS 'Authoritative verified identity when the phase required verification; NULL otherwise. No FK so privacy deletion of a verified identity is independent.';
-- Set comment to column: "resale_without_consent_prohibited" on table: "tickets"
COMMENT ON COLUMN "tickets"."resale_without_consent_prohibited" IS 'Covered-ticket condition (i): always true (enforced by CHECK) so every issued ticket qualifies as a 特定興行入場券';
-- Set comment to column: "status" on table: "tickets"
COMMENT ON COLUMN "tickets"."status" IS 'Ticket status: 1=Issued, 2=Voided (on refund)';
-- Set comment to column: "issued_at" on table: "tickets"
COMMENT ON COLUMN "tickets"."issued_at" IS 'When the ticket was issued (= the order capture/issuance time)';
