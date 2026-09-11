-- Drop ticket_emails table and its index.
-- The ticket-email import capability (PWA Share Target + Gemini parser) has
-- been removed. The ticket_emails table held Gemini-parsed email data that
-- was used to seed ticket_journeys; ticket_journeys itself is preserved
-- because the ticket-journey tracking feature (manual SetStatus, sales
-- reminders, sales-phase announcements) remains active.

-- The index is dropped implicitly when the table is dropped, but we drop it
-- explicitly first to be consistent with how Atlas handles index removal.
DROP INDEX IF EXISTS idx_ticket_emails_user_event;

DROP TABLE IF EXISTS ticket_emails;
