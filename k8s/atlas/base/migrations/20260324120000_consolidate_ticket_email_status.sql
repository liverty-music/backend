-- Consolidate lottery_result and payment_status into a single journey_status column.
-- TicketEmail now tracks a single TicketJourneyStatus derived from the email type and content.
--
-- Idempotent: dev DB may already have journey_status (from an in-place edit of 20260319) and
-- lottery_result/payment_status already dropped. Each ALTER is guarded accordingly.
-- Note: DROP COLUMN automatically drops dependent CHECK constraints in PostgreSQL.
DO $$
BEGIN
    -- Add journey_status if it doesn't exist yet
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'app' AND table_name = 'ticket_emails' AND column_name = 'journey_status'
    ) THEN
        ALTER TABLE ticket_emails ADD COLUMN journey_status SMALLINT;
    END IF;

    -- Drop lottery_result if it still exists
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'app' AND table_name = 'ticket_emails' AND column_name = 'lottery_result'
    ) THEN
        ALTER TABLE ticket_emails DROP COLUMN lottery_result;
    END IF;

    -- Drop payment_status if it still exists
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'app' AND table_name = 'ticket_emails' AND column_name = 'payment_status'
    ) THEN
        ALTER TABLE ticket_emails DROP COLUMN payment_status;
    END IF;

    -- Add journey_status constraint if it doesn't exist yet
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        JOIN pg_class t ON c.conrelid = t.oid
        JOIN pg_namespace n ON t.relnamespace = n.oid
        WHERE n.nspname = 'app' AND t.relname = 'ticket_emails' AND c.conname = 'chk_ticket_emails_journey_status'
    ) THEN
        ALTER TABLE ticket_emails
            ADD CONSTRAINT chk_ticket_emails_journey_status
                CHECK (journey_status IS NULL OR journey_status BETWEEN 1 AND 5);
    END IF;
END $$;

COMMENT ON COLUMN ticket_emails.journey_status IS 'TicketJourney status derived from email: 1=TRACKING, 2=APPLIED, 3=LOST, 4=UNPAID, 5=PAID';
