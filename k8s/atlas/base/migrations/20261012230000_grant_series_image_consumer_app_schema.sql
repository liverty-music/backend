-- Grant app-schema privileges to the `series-image-consumer` Cloud SQL IAM
-- service-account user.
--
-- The series image worker is renamed from media-consumer to
-- series-image-consumer. It runs under its own GSA and authenticates to Cloud
-- SQL as `series-image-consumer@<project>.iam` (created by Pulumi in
-- postgres.ts before this migration applies). It performs the
-- series_cover_images cut-over after generating WebP variants, so it needs the
-- same read+write on the app schema that
-- 20260901000000_grant_media_consumer_app_schema.sql gave the old user. Every
-- grant loop since then names its roles, so a role created after 20260901000000
-- holds nothing until a migration grants it.
--
-- The grant covers existing objects (GRANT ... ON ALL ... IN SCHEMA app) and
-- future ones (ALTER DEFAULT PRIVILEGES for tables the postgres user creates),
-- and is a no-op when re-run. The loop matches only this workload's IAM user
-- and skips it when the role does not exist yet.
--
-- The old media-consumer user keeps its privileges until the cleanup that
-- removes it revokes them.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN SELECT rolname FROM pg_roles WHERE rolname LIKE 'series-image-consumer@%.iam' LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA app TO %I', iam_role);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA app TO %I', iam_role);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA app TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT USAGE, SELECT ON SEQUENCES TO %I', iam_role);
  END LOOP;
END
$$;
