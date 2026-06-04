-- Create "sales_phases" table
CREATE TABLE "sales_phases" (
  "id" uuid NOT NULL,
  "series_id" uuid NOT NULL,
  "anchor_event_id" uuid NOT NULL,
  "method" smallint NOT NULL,
  "channel" smallint NOT NULL,
  "provider_name" text NULL,
  "sequence" integer NOT NULL DEFAULT 0,
  "apply_start_at" timestamptz NOT NULL,
  "apply_end_at" timestamptz NULL,
  "lottery_result_at" timestamptz NULL,
  "payment_deadline_at" timestamptz NULL,
  "url" text NULL,
  "discovered_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_sales_phases_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'),
  CONSTRAINT "chk_sales_phases_method" CHECK (method BETWEEN 0 AND 2),
  CONSTRAINT "chk_sales_phases_channel" CHECK (channel BETWEEN 0 AND 6),
  CONSTRAINT "chk_sales_phases_sequence" CHECK (sequence >= 0),
  CONSTRAINT "sales_phases_series_id_fkey" FOREIGN KEY ("series_id") REFERENCES "series" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "sales_phases_anchor_event_id_fkey" FOREIGN KEY ("anchor_event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Set comment to table: "sales_phases"
COMMENT ON TABLE "sales_phases" IS 'A single ticket-sales window for a series. The surrogate id is the only uniqueness key; application-layer overlap matching converges re-discovered phases onto existing rows.';
-- Set comment to column: "id" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."id" IS 'Unique sales phase identifier (UUIDv7, application-generated)';
-- Set comment to column: "series_id" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."series_id" IS 'Reference to the parent series that owns this sales phase';
-- Set comment to column: "anchor_event_id" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."anchor_event_id" IS 'Earliest covered event at insert time; immutable — never recomputed after initial write. Used as a stable tiebreaker when matching on covered-event overlap.';
-- Set comment to column: "method" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."method" IS 'Sales method: 0=UNSPECIFIED, 1=LOTTERY, 2=FIRST_COME';
-- Set comment to column: "channel" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."channel" IS 'Sales channel: 0=UNSPECIFIED, 1=FAN_CLUB, 2=OFFICIAL, 3=PLAYGUIDE, 4=CREDIT_CARD, 5=MOBILE_CARRIER, 6=GENERAL. Concrete play-guide provider names go in provider_name.';
-- Set comment to column: "provider_name" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."provider_name" IS 'Verbatim provider name from the source (e.g. "e+", "ローチケ"). NULL when indeterminate.';
-- Set comment to column: "sequence" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."sequence" IS 'Ordinal within the same channel for phases that occur in multiple rounds (0-based). Does not uniquely identify a phase.';
-- Set comment to column: "apply_start_at" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."apply_start_at" IS 'Start of the application or on-sale window (required). Must be known for a phase to be persisted.';
-- Set comment to column: "apply_end_at" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."apply_end_at" IS 'End of the application window (lottery) or close of on-sale (first-come). NULL when unknown.';
-- Set comment to column: "lottery_result_at" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."lottery_result_at" IS 'When lottery results are announced. NULL for FCFS phases or when unknown.';
-- Set comment to column: "payment_deadline_at" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."payment_deadline_at" IS 'Payment deadline after winning a lottery. NULL for FCFS phases or when unknown.';
-- Set comment to column: "url" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."url" IS 'Direct URL to the sales page for this phase. NULL when not available.';
-- Set comment to column: "discovered_at" on table: "sales_phases"
COMMENT ON COLUMN "sales_phases"."discovered_at" IS 'Timestamp when this sales phase row was first inserted. Used as the first-sight guard: stages whose natural trigger is before discovered_at are not fired (the phase was discovered after that milestone already passed).';
-- Create index "idx_sales_phases_series_id" to table: "sales_phases"
CREATE INDEX "idx_sales_phases_series_id" ON "sales_phases" ("series_id");
-- Set comment to index: "idx_sales_phases_series_id" on table: "sales_phases"
COMMENT ON INDEX "idx_sales_phases_series_id" IS 'Optimizes listing all sales phases for a series';
-- Create index "idx_sales_phases_apply_start_at" to table: "sales_phases"
CREATE INDEX "idx_sales_phases_apply_start_at" ON "sales_phases" ("apply_start_at");
-- Set comment to index: "idx_sales_phases_apply_start_at" on table: "sales_phases"
COMMENT ON INDEX "idx_sales_phases_apply_start_at" IS 'Supports ListUpcomingByDueWindow queries that filter by apply_start_at range';
-- Create "event_sales_phases" table
CREATE TABLE "event_sales_phases" (
  "sales_phase_id" uuid NOT NULL,
  "event_id" uuid NOT NULL,
  PRIMARY KEY ("sales_phase_id", "event_id"),
  CONSTRAINT "event_sales_phases_sales_phase_id_fkey" FOREIGN KEY ("sales_phase_id") REFERENCES "sales_phases" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "event_sales_phases_event_id_fkey" FOREIGN KEY ("event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Set comment to table: "event_sales_phases"
COMMENT ON TABLE "event_sales_phases" IS 'M:N join between sales_phases and events. Populated and replaced atomically by the repository on every upsert so incremental coverage growth is handled in-place without duplicating the phase row.';
-- Set comment to column: "sales_phase_id" on table: "event_sales_phases"
COMMENT ON COLUMN "event_sales_phases"."sales_phase_id" IS 'Reference to the sales phase';
-- Set comment to column: "event_id" on table: "event_sales_phases"
COMMENT ON COLUMN "event_sales_phases"."event_id" IS 'Reference to the covered event';
-- Create index "idx_event_sales_phases_event_id" to table: "event_sales_phases"
CREATE INDEX "idx_event_sales_phases_event_id" ON "event_sales_phases" ("event_id");
-- Set comment to index: "idx_event_sales_phases_event_id" on table: "event_sales_phases"
COMMENT ON INDEX "idx_event_sales_phases_event_id" IS 'Optimizes lookup of all sales phases covering a given event';
-- Create "sales_phase_reminders" table
CREATE TABLE "sales_phase_reminders" (
  "id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "sales_phase_id" uuid NOT NULL,
  "stage" smallint NOT NULL,
  "sent_at" timestamptz NOT NULL DEFAULT NOW(),
  PRIMARY KEY ("id"),
  CONSTRAINT "uq_sales_phase_reminders" UNIQUE ("user_id", "sales_phase_id", "stage"),
  CONSTRAINT "chk_sales_phase_reminders_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'),
  CONSTRAINT "chk_sales_phase_reminders_stage" CHECK (stage BETWEEN 1 AND 10),
  CONSTRAINT "sales_phase_reminders_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "sales_phase_reminders_sales_phase_id_fkey" FOREIGN KEY ("sales_phase_id") REFERENCES "sales_phases" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Set comment to table: "sales_phase_reminders"
COMMENT ON TABLE "sales_phase_reminders" IS 'Sent-log for sales phase reminder notifications. UNIQUE (user_id, sales_phase_id, stage) prevents duplicate dispatches.';
-- Set comment to column: "id" on table: "sales_phase_reminders"
COMMENT ON COLUMN "sales_phase_reminders"."id" IS 'Unique reminder record identifier (UUIDv7, application-generated)';
-- Set comment to column: "user_id" on table: "sales_phase_reminders"
COMMENT ON COLUMN "sales_phase_reminders"."user_id" IS 'Reference to the user who received the reminder';
-- Set comment to column: "sales_phase_id" on table: "sales_phase_reminders"
COMMENT ON COLUMN "sales_phase_reminders"."sales_phase_id" IS 'Reference to the sales phase this reminder relates to';
-- Set comment to column: "stage" on table: "sales_phase_reminders"
COMMENT ON COLUMN "sales_phase_reminders"."stage" IS 'Reminder stage: 1=APPLY_OPEN (at apply_start_time), 2=APPLY_CLOSE_24H (24h before apply_end_time), 3=APPLY_CLOSE_1H (1h before apply_end_time), 4=RESULT_DAY (09:00 on lottery_result_time day). Payment-deadline stage deferred (win/loss gating out of scope).';
-- Set comment to column: "sent_at" on table: "sales_phase_reminders"
COMMENT ON COLUMN "sales_phase_reminders"."sent_at" IS 'Timestamp when the reminder was dispatched';
-- Create index "idx_sales_phase_reminders_user_id" to table: "sales_phase_reminders"
CREATE INDEX "idx_sales_phase_reminders_user_id" ON "sales_phase_reminders" ("user_id");
-- Set comment to index: "idx_sales_phase_reminders_user_id" on table: "sales_phase_reminders"
COMMENT ON INDEX "idx_sales_phase_reminders_user_id" IS 'Optimizes lookup of all reminders for a user';
-- Create index "idx_sales_phase_reminders_sales_phase_id" to table: "sales_phase_reminders"
CREATE INDEX "idx_sales_phase_reminders_sales_phase_id" ON "sales_phase_reminders" ("sales_phase_id");
-- Set comment to index: "idx_sales_phase_reminders_sales_phase_id" on table: "sales_phase_reminders"
COMMENT ON INDEX "idx_sales_phase_reminders_sales_phase_id" IS 'Optimizes lookup of all reminder records for a sales phase';
