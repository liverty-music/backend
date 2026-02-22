-- Grant app schema permissions to all Cloud SQL IAM-authenticated users.
-- The original bootstrap migration only matched '%@%.iam' (Workload Identity SAs).
-- This migration extends access to human IAM users (CLOUD_IAM_USER type)
-- so they can browse the app schema via Cloud SQL Studio and other tools.

DO $$
DECLARE
  iam_role TEXT;
BEGIN
  -- Match all IAM-authenticated roles: both service accounts (user@project.iam)
  -- and human users (user@domain.tld) registered via CLOUD_IAM_USER.
  FOR iam_role IN SELECT rolname FROM pg_roles WHERE rolname LIKE '%@%' LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA app TO %I', iam_role);
    EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA app TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT ON TABLES TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT USAGE, SELECT ON SEQUENCES TO %I', iam_role);
  END LOOP;
END
$$;
