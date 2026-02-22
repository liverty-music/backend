-- Bootstrap: create app schema and grant permissions to IAM service account.
-- This migration runs as the postgres (cloudsqlsuperuser) user via Atlas Operator.

CREATE SCHEMA IF NOT EXISTS app;

-- Grant schema-level permissions to backend IAM SA.
-- The role name follows Cloud SQL IAM format: SA_NAME@PROJECT_ID.iam
-- This is parameterized per environment via the actual IAM SA bound to the instance.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  -- Grant schema-level permissions to all IAM SA roles.
  -- Cloud SQL creates them as SA_NAME@PROJECT.iam
  FOR iam_role IN SELECT rolname FROM pg_roles WHERE rolname LIKE '%@%.iam' LOOP
    EXECUTE format('GRANT USAGE, CREATE ON SCHEMA app TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT ALL ON TABLES TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT ALL ON SEQUENCES TO %I', iam_role);
  END LOOP;
END
$$;
