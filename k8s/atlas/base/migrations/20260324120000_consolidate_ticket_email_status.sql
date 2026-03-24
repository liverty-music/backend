-- Consolidate lottery_result and payment_status into a single journey_status column.
-- TicketEmail now tracks a single TicketJourneyStatus derived from the email type and content.
-- Note: DROP COLUMN automatically drops dependent CHECK constraints in PostgreSQL,
-- so chk_ticket_emails_lottery_result and chk_ticket_emails_payment_status are removed implicitly.
ALTER TABLE ticket_emails
    ADD COLUMN journey_status SMALLINT,
    DROP COLUMN lottery_result,
    DROP COLUMN payment_status;

ALTER TABLE ticket_emails
    ADD CONSTRAINT chk_ticket_emails_journey_status CHECK (journey_status IS NULL OR journey_status BETWEEN 1 AND 5);

COMMENT ON COLUMN ticket_emails.journey_status IS 'TicketJourney status derived from email: 1=TRACKING, 2=APPLIED, 3=LOST, 4=UNPAID, 5=PAID';
