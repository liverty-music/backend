-- Grant the `organizer-console-api` Cloud SQL IAM service-account user the
-- writes the reception paths of the organizer server need
-- (ticket-wallet-and-checkin). The role is read-only by default
-- (20260825000000 / 20260825120000); grants stay as narrow as the paths:
--
--   reception_links     — Issue (INSERT), Open binds a device and Revoke
--                         (UPDATE)
--   tickets.admitted_at — Ticket.Admit sets the admitted time (column-level
--                         UPDATE only; no other ticket column is writable)
--   admissions          — Ticket.Admit (INSERT only: append-only evidence)
--   rejected_scans      — RejectedScan.Append (INSERT only: append-only)
--
-- No UPDATE or DELETE is granted on admissions or rejected_scans, so the
-- organizer server cannot alter either record.
--
-- Pattern mirrors 20260826040000: loop over pg_roles so the migration is
-- idempotent and skips the role where it does not exist yet.
SET search_path TO app, public;

DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('GRANT INSERT, UPDATE ON app.reception_links TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (admitted_at) ON app.tickets TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.admissions TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.rejected_scans TO %I', iam_role);
  END LOOP;
END
$$;
