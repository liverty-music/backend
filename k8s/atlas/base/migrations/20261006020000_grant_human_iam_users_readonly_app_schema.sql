-- Grant read-only app-schema privileges to human Cloud SQL IAM users.
--
-- Developers reach Cloud SQL through the ephemeral `db-proxy` Pod
-- (cloud-provisioning k8s/tools/db-proxy) and log in as their own
-- CLOUD_IAM_USER (`<name>@<domain>`) with a `gcloud sql generate-login-token`
-- password. They are read-only by default; writes go through the `postgres`
-- break-glass login. See OpenSpec change `unify-cloud-sql-access` (D4).
--
-- 20260223120000 gave the same read access to every role matching '%@%' that
-- existed when it ran, but later grant loops match only service accounts
-- ('%@%.iam'), so a human user created after that migration first ran holds no
-- grants. This loop covers human users only: Cloud SQL names IAM
-- service-account users `<name>@<project>.iam`, so they are excluded and keep
-- their own (read-write or read-only) grants.
--
-- It covers BOTH existing objects (GRANT ... ON ALL ... IN SCHEMA app) and
-- future ones (ALTER DEFAULT PRIVILEGES, which applies to objects created by
-- the role running this migration: postgres, the owner of every app object via
-- the Atlas Operator). Idempotent: re-granting is a no-op. A human user added
-- later is granted by running the same block once as postgres (the
-- cloud-provisioning runbook docs/runbooks/cloud-sql-access.md carries it).
DO $$
DECLARE
  human_role TEXT;
BEGIN
  FOR human_role IN
    SELECT rolname FROM pg_roles
    WHERE rolname LIKE '%@%' AND rolname NOT LIKE '%.iam'
  LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA app TO %I', human_role);
    EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA app TO %I', human_role);
    EXECUTE format('GRANT SELECT ON ALL SEQUENCES IN SCHEMA app TO %I', human_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT ON TABLES TO %I', human_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT ON SEQUENCES TO %I', human_role);
  END LOOP;
END
$$;
