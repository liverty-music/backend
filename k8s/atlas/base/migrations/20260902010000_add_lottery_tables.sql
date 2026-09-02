-- Create "lottery_sales_phases" table
CREATE TABLE "lottery_sales_phases" (
  "id" uuid NOT NULL,
  "event_id" uuid NOT NULL,
  "open_at" timestamptz NOT NULL,
  "close_at" timestamptz NOT NULL,
  "ticket_capacity" integer NOT NULL,
  "max_tickets_per_application" integer NOT NULL,
  "ticket_price" bigint NOT NULL,
  "drawn_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "lottery_sales_phases_event_id_fkey" FOREIGN KEY ("event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "chk_lottery_sales_phases_capacity_positive" CHECK (ticket_capacity > 0),
  CONSTRAINT "chk_lottery_sales_phases_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_lottery_sales_phases_max_positive" CHECK (max_tickets_per_application > 0),
  CONSTRAINT "chk_lottery_sales_phases_price_positive" CHECK (ticket_price > 0),
  CONSTRAINT "chk_lottery_sales_phases_window" CHECK (close_at > open_at)
);
-- Create index "idx_lottery_sales_phases_event_id" to table: "lottery_sales_phases"
CREATE INDEX "idx_lottery_sales_phases_event_id" ON "lottery_sales_phases" ("event_id");
-- Set comment to table: "lottery_sales_phases"
COMMENT ON TABLE "lottery_sales_phases" IS 'Organizer-configured lottery sales windows for published concert events. Each row carries the full lottery parameters (capacity, max-per-application, price) needed to run the draw.';
-- Set comment to column: "id" on table: "lottery_sales_phases"
COMMENT ON COLUMN "lottery_sales_phases"."id" IS 'Unique phase identifier (UUIDv7, application-generated)';
-- Set comment to column: "event_id" on table: "lottery_sales_phases"
COMMENT ON COLUMN "lottery_sales_phases"."event_id" IS 'Published concert event this lottery phase is attached to';
-- Set comment to column: "open_at" on table: "lottery_sales_phases"
COMMENT ON COLUMN "lottery_sales_phases"."open_at" IS 'When the application window opens';
-- Set comment to column: "close_at" on table: "lottery_sales_phases"
COMMENT ON COLUMN "lottery_sales_phases"."close_at" IS 'When the application window closes. Must be after open_at.';
-- Set comment to column: "ticket_capacity" on table: "lottery_sales_phases"
COMMENT ON COLUMN "lottery_sales_phases"."ticket_capacity" IS 'Total tickets available in this phase. Must be positive.';
-- Set comment to column: "max_tickets_per_application" on table: "lottery_sales_phases"
COMMENT ON COLUMN "lottery_sales_phases"."max_tickets_per_application" IS 'Maximum companion-group size a single fan can request. Must be positive.';
-- Set comment to column: "ticket_price" on table: "lottery_sales_phases"
COMMENT ON COLUMN "lottery_sales_phases"."ticket_price" IS 'Per-ticket price in JPY (whole yen). Must be positive. Authorization amount = ticket_price × requested_ticket_count.';
-- Set comment to column: "drawn_at" on table: "lottery_sales_phases"
COMMENT ON COLUMN "lottery_sales_phases"."drawn_at" IS 'Timestamp when the draw ran for this phase. NULL means the draw has not yet run. Set atomically by PersistDrawOutcome; acts as an idempotency guard: the draw sweeper skips phases where drawn_at IS NOT NULL.';
-- Set comment to index: "idx_lottery_sales_phases_event_id" on table: "lottery_sales_phases"
COMMENT ON INDEX "idx_lottery_sales_phases_event_id" IS 'Optimizes listing lottery phases for a given event';
-- Create "ticket_applications" table
CREATE TABLE "ticket_applications" (
  "id" uuid NOT NULL,
  "phase_id" uuid NOT NULL,
  "applicant_id" uuid NOT NULL,
  "requested_ticket_count" integer NOT NULL,
  "applicant_full_name" text NOT NULL,
  "applicant_phone_number" text NOT NULL,
  "payment_intent_ref" text NOT NULL,
  "state" smallint NOT NULL,
  "draw_sequence" bigint NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "ticket_applications_phase_id_fkey" FOREIGN KEY ("phase_id") REFERENCES "lottery_sales_phases" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "chk_ticket_applications_count_positive" CHECK (requested_ticket_count > 0),
  CONSTRAINT "chk_ticket_applications_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text),
  CONSTRAINT "chk_ticket_applications_state" CHECK (state = ANY (ARRAY[1, 2, 3, 5]))
);
-- Create index "idx_ticket_applications_phase_id" to table: "ticket_applications"
CREATE INDEX "idx_ticket_applications_phase_id" ON "ticket_applications" ("phase_id");
-- Create index "uq_ticket_applications_active" to table: "ticket_applications"
CREATE UNIQUE INDEX "uq_ticket_applications_active" ON "ticket_applications" ("phase_id", "applicant_id") WHERE (state = ANY (ARRAY[1, 2, 3]));
-- Set comment to table: "ticket_applications"
COMMENT ON TABLE "ticket_applications" IS 'Fan applications to a lottery sales phase. One row per attempt; re-application after withdrawal creates a fresh row. State: 1=Applied, 2=Won, 3=Lost, 5=Withdrawn.';
-- Set comment to column: "id" on table: "ticket_applications"
COMMENT ON COLUMN "ticket_applications"."id" IS 'Unique application identifier (UUIDv7, application-generated)';
-- Set comment to column: "phase_id" on table: "ticket_applications"
COMMENT ON COLUMN "ticket_applications"."phase_id" IS 'The lottery phase this application belongs to';
-- Set comment to column: "applicant_id" on table: "ticket_applications"
COMMENT ON COLUMN "ticket_applications"."applicant_id" IS 'The fan user ID (references users.id at application layer; no FK to survive user lifecycle independently)';
-- Set comment to column: "requested_ticket_count" on table: "ticket_applications"
COMMENT ON COLUMN "ticket_applications"."requested_ticket_count" IS 'Companion-group size (all-or-nothing allocation). Must be positive.';
-- Set comment to column: "applicant_full_name" on table: "ticket_applications"
COMMENT ON COLUMN "ticket_applications"."applicant_full_name" IS 'Applicant legal name for 本人確認 at the venue';
-- Set comment to column: "applicant_phone_number" on table: "ticket_applications"
COMMENT ON COLUMN "ticket_applications"."applicant_phone_number" IS 'Contact phone number for 本人確認 at the venue';
-- Set comment to column: "payment_intent_ref" on table: "ticket_applications"
COMMENT ON COLUMN "ticket_applications"."payment_intent_ref" IS 'Stripe PaymentIntent ID for the authorization hold placed at apply time';
-- Set comment to column: "state" on table: "ticket_applications"
COMMENT ON COLUMN "ticket_applications"."state" IS 'Lifecycle state: 1=Applied (hold in place), 2=Won (captured), 3=Lost (hold released), 5=Withdrawn (fan-cancelled, hold released)';
-- Set comment to column: "draw_sequence" on table: "ticket_applications"
COMMENT ON COLUMN "ticket_applications"."draw_sequence" IS 'Zero-based position in the draw shuffle. NULL until the draw runs; used to order the loser waitlist for official-resale.';
-- Set comment to index: "idx_ticket_applications_phase_id" on table: "ticket_applications"
COMMENT ON INDEX "idx_ticket_applications_phase_id" IS 'Optimizes listing all applications for a given lottery phase (draw batch load)';
-- Set comment to index: "uq_ticket_applications_active" on table: "ticket_applications"
COMMENT ON INDEX "uq_ticket_applications_active" IS 'At most one active (Applied/Won/Lost) application per (phase, applicant). Withdrawn (5) is excluded so the fan can re-apply. Won/Lost are included: a concluded draw outcome still uniquely identifies participation.';
