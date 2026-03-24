-- Create ticket_emails table for imported ticket-related emails parsed by Gemini Flash
CREATE TABLE ticket_emails (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    email_type SMALLINT NOT NULL,
    raw_body TEXT NOT NULL,
    parsed_data JSONB,
    payment_deadline_at TIMESTAMPTZ,
    lottery_start_at TIMESTAMPTZ,
    lottery_end_at TIMESTAMPTZ,
    application_url TEXT,
    lottery_result SMALLINT,
    payment_status SMALLINT,
    CONSTRAINT chk_ticket_emails_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_ticket_emails_email_type CHECK (email_type BETWEEN 1 AND 2),
    CONSTRAINT chk_ticket_emails_lottery_result CHECK (lottery_result IS NULL OR lottery_result BETWEEN 1 AND 2),
    CONSTRAINT chk_ticket_emails_payment_status CHECK (payment_status IS NULL OR payment_status BETWEEN 1 AND 2)
);

COMMENT ON TABLE ticket_emails IS 'Ticket-related emails imported via PWA Share Target and parsed by Gemini Flash. Linked to ticket_journeys via (user_id, event_id).';
COMMENT ON COLUMN ticket_emails.id IS 'Unique ticket email identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN ticket_emails.user_id IS 'Reference to the fan who imported this email';
COMMENT ON COLUMN ticket_emails.event_id IS 'Reference to the event this email is associated with';
COMMENT ON COLUMN ticket_emails.email_type IS 'Email type: 1=LOTTERY_INFO, 2=LOTTERY_RESULT';
COMMENT ON COLUMN ticket_emails.raw_body IS 'Email text as provided by the user (optionally redacted for PII)';
COMMENT ON COLUMN ticket_emails.parsed_data IS 'Structured JSON output from Gemini Flash parsing';
COMMENT ON COLUMN ticket_emails.payment_deadline_at IS 'Payment due date extracted from lottery result emails';
COMMENT ON COLUMN ticket_emails.lottery_start_at IS 'Lottery application period start from lottery info emails';
COMMENT ON COLUMN ticket_emails.lottery_end_at IS 'Lottery application period end from lottery info emails';
COMMENT ON COLUMN ticket_emails.application_url IS 'URL for lottery application from lottery info emails';
COMMENT ON COLUMN ticket_emails.lottery_result IS 'Lottery outcome: 1=WON, 2=LOST. Present only for LOTTERY_RESULT emails.';
COMMENT ON COLUMN ticket_emails.payment_status IS 'Payment state: 1=UNPAID, 2=PAID. Present only for LOTTERY_RESULT emails where lottery_result=WON.';

CREATE INDEX idx_ticket_emails_user_event ON ticket_emails(user_id, event_id);
COMMENT ON INDEX idx_ticket_emails_user_event IS 'Optimizes lookup of imported emails for a user-event combination';
