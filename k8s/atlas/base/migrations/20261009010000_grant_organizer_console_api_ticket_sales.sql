-- Grant the `organizer-console-api` Cloud SQL IAM service-account user the
-- writes the ticket sale paths of the organizer server need
-- (first-come-ticket-sales). The role is read-only by default
-- (20261008020000); grants stay as narrow as the paths:
--
--   ticket_sales — TicketSale Configure creates the event's sale (INSERT) and
--                  changes its window, price, quantity and per-account limit
--                  (column-level UPDATE). The column privilege also covers
--                  the sale row lock (SELECT ... FOR UPDATE) Update takes.
--
-- sold_count stays read-only for the role: only checkouts (the fan server)
-- move it. Reservations and the outbox stay SELECT-only.
--
-- Pattern mirrors 20261008010000: loop over pg_roles so the migration is
-- idempotent and skips the role where it does not exist yet.
SET search_path TO app, public;

DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('GRANT INSERT ON app.ticket_sales TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (sale_start_at, sale_end_at, price, quantity, per_account_limit) ON app.ticket_sales TO %I', iam_role);
  END LOOP;
END
$$;
