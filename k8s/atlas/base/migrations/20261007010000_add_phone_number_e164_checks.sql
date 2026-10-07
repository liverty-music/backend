-- Enforce E.164 on stored applicant / ticket-holder phone numbers
-- (modernize-protobuf-workflow, design D7). The previous migration normalized
-- every convertible row; VALIDATE CONSTRAINT fails the deploy if any row is
-- still in another format.

ALTER TABLE ticket_applications
    ADD CONSTRAINT chk_ticket_applications_applicant_phone_e164
    CHECK (applicant_phone_number ~ '^\+[1-9][0-9]{1,14}$') NOT VALID;
ALTER TABLE ticket_applications
    VALIDATE CONSTRAINT chk_ticket_applications_applicant_phone_e164;

ALTER TABLE tickets
    ADD CONSTRAINT chk_tickets_holder_phone_e164
    CHECK (holder_phone_number ~ '^\+[1-9][0-9]{1,14}$') NOT VALID;
ALTER TABLE tickets
    VALIDATE CONSTRAINT chk_tickets_holder_phone_e164;

COMMENT ON COLUMN ticket_applications.applicant_phone_number IS 'Contact phone number for 本人確認 at the venue, in E.164 form (enforced by CHECK)';
COMMENT ON COLUMN tickets.holder_phone_number IS 'Holder contact phone (本人確認), in E.164 form (enforced by CHECK)';
