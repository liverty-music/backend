-- Grant app-schema privileges to the `fan-api` Cloud SQL IAM service-account user.
--
-- `fan-api` is the audience-tier successor to `backend-app` (unify-workload-naming).
-- It authenticates to Cloud SQL as its own IAM DB user (`fan-api@<project>.iam`,
-- created by Pulumi in postgres.ts) and runs the same backend binary, so it needs
-- the same app-schema access as backend-app before `DATABASE_USER` flips to fan-api
-- at the fan cutover.
--
-- The earlier grant migrations (bootstrap + 20260816000000_grant_admin_console_api…)
-- only granted the IAM roles that existed when they ran. `fan-api@<project>.iam` was
-- created afterwards, so it holds no privileges on the existing tables or future
-- ones. This re-runs the same generic, idempotent grant loop over every current IAM
-- role, covering BOTH existing objects (GRANT ... ON ALL ... IN SCHEMA app) and
-- future ones (ALTER DEFAULT PRIVILEGES). It is a no-op for roles already granted.
--
-- Runs as the postgres (cloudsqlsuperuser) user via the Atlas Operator. The fan-api
-- Cloud SQL IAM user must already exist (Pulumi `pulumi up` creates it before this
-- migration applies); the loop simply skips any IAM role that is not yet present.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  -- Cloud SQL registers IAM service-account users as `<name>@<project>.iam`.
  FOR iam_role IN SELECT rolname FROM pg_roles WHERE rolname LIKE '%@%.iam' LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA app TO %I', iam_role);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA app TO %I', iam_role);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA app TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT USAGE, SELECT ON SEQUENCES TO %I', iam_role);
  END LOOP;
END
$$;
