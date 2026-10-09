-- Give the `reception-api` Cloud SQL IAM service-account user exactly the
-- reception reads and writes, and take the reception-only writes away from
-- `organizer-console-api` (isolate-venue-reception).
--
-- WHY: ReceptionService (Open, Admit) is unauthenticated and now runs on its
-- own reception server, exposed only by the reception-api workload. The
-- organizer console role no longer serves it, so it loses the writes only
-- reception performed (20261008010000 / 20261008020000).
--
-- reception-api (traced from the reception server in internal/di/provider.go
-- through ReceptionLinkUseCase.Open, TicketUseCase.Admit and their rdb
-- repositories):
--
--   reception_links     SELECT; UPDATE (status, bound_public_key, bound_at,
--                       token) — Open binds the device (FOR UPDATE); Admit
--                       locks the link (FOR SHARE). Row locks need UPDATE on
--                       at least one column.
--   events              SELECT — the reception window
--   wallet_public_keys  SELECT — verify the admission code
--   tickets             SELECT; UPDATE (admitted_at) — Ticket.Admit
--   admissions          SELECT, INSERT — Ticket.Admit (append-only) and the
--                       earlier admission of an already admitted ticket
--   rejected_scans      INSERT — RejectedScan.Append (append-only)
--
-- No other table, no sequence and no default privilege: a table added later
-- stays out of reach until a migration grants it by name.
--
-- organizer-console-api keeps INSERT and UPDATE (status, token, revoked_at)
-- on reception_links for ReceptionLink Issue / Revoke, and loses UPDATE
-- (bound_public_key, bound_at) on reception_links, UPDATE (admitted_at) on
-- tickets, and INSERT on admissions and rejected_scans.
--
-- Earlier generic `%@%.iam` grant loops reach any IAM role that exists when
-- they run, so the reception-api grant starts by revoking everything. Any
-- future generic loop MUST also append
--   AND rolname NOT LIKE 'reception-api@%'
-- next to the organizer-console-api and zitadel exclusions (20261008020000).
-- rdb.TestMigrationGrants_IAMRoles fails if the role gains anything else.
--
-- Table names are resolved through the search_path below rather than
-- qualified: production keeps every table in app, while a database migrated
-- without that search_path (CI's test-db) has the older tables in public.
--
-- Idempotent; each loop skips a role that does not exist in the environment.
SET search_path TO app, public;

DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'reception-api@%.iam'
  LOOP
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA app FROM %I', iam_role);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA app FROM %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app REVOKE ALL ON TABLES FROM %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app REVOKE ALL ON SEQUENCES FROM %I', iam_role);

    EXECUTE format('GRANT USAGE ON SCHEMA app TO %I', iam_role);
    EXECUTE format('GRANT SELECT ON reception_links, events, wallet_public_keys, tickets, admissions TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (status, bound_public_key, bound_at, token) ON reception_links TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (admitted_at) ON tickets TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON admissions TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON rejected_scans TO %I', iam_role);
  END LOOP;

  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('REVOKE UPDATE (bound_public_key, bound_at) ON reception_links FROM %I', iam_role);
    EXECUTE format('REVOKE UPDATE (admitted_at) ON tickets FROM %I', iam_role);
    EXECUTE format('REVOKE INSERT ON admissions FROM %I', iam_role);
    EXECUTE format('REVOKE INSERT ON rejected_scans FROM %I', iam_role);
  END LOOP;
END
$$;
