-- publish-events-through-outbox (tasks 8.0): requests start workflows. The
-- event consumer and the jobs, which run as the `backend-app` IAM user, start
-- them (NotifyNewConcerts, AnnounceDiscoveredSale, SendOrderConfirmation,
-- ScanDueReminders, SearchNewConcerts and SearchNewConcertsOnFirstFollow), and
-- so does the admin console API, as `admin-console-api` (SearchNewConcerts and
-- the notifications an admin sends). Those roles get the grants of
-- `dbos migrate --print-user-role`, each by name, as
-- 20261027120000_create_dbos_schema.sql gives them to `fan-api`. The organizer
-- console and reception APIs start none and get nothing.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN SELECT rolname FROM pg_roles
                  WHERE rolname LIKE 'backend-app@%.iam' OR rolname LIKE 'admin-console-api@%.iam' LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA "dbos" TO %I', iam_role);
    EXECUTE format('GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA "dbos" TO %I', iam_role);
    EXECUTE format('GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA "dbos" TO %I', iam_role);
    EXECUTE format('GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA "dbos" TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA "dbos" GRANT ALL ON TABLES TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA "dbos" GRANT ALL ON SEQUENCES TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA "dbos" GRANT EXECUTE ON FUNCTIONS TO %I', iam_role);
  END LOOP;
END
$$;
