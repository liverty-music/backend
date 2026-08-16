-- Grant app-schema privileges to Cloud SQL IAM service-account users added after
-- the original bootstrap (20250726000000_bootstrap_app_schema.sql).
--
-- The isolated `admin-console-api` workload authenticates to Cloud SQL as its own
-- IAM DB user (`admin-console-api@<project>.iam`, created by Pulumi in
-- postgres.ts) rather than reusing `backend-app`. It runs the same backend binary
-- and needs the same app-schema access to serve the admin Organizer RPCs and run
-- the provisioning reconciler.
--
-- The bootstrap migration only set ALTER DEFAULT PRIVILEGES for the IAM roles that
-- existed at that time, so a newly-added IAM user receives no privileges on the
-- already-created tables (organizers, organizer_artists, ...) and nothing on
-- future ones. This re-runs the grant over every current IAM role, covering BOTH
-- existing objects (GRANT ... ON ALL ... IN SCHEMA app) and future ones
-- (ALTER DEFAULT PRIVILEGES). It is idempotent for backend-app.
--
-- Runs as the postgres (cloudsqlsuperuser) user via the Atlas Operator. The
-- admin-console-api Cloud SQL IAM user must already exist (Pulumi `pulumi up`
-- creates it before this migration applies); the loop simply skips any IAM role
-- that is not yet present.
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
