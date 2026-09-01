-- Grant app-schema privileges to the `media-consumer` Cloud SQL IAM service-account user.
--
-- The media-consumer (renamed from the broken media-processor ScaledJob in
-- declarative-jetstream-nack) runs under its OWN dedicated GSA and authenticates
-- to Cloud SQL as `media-consumer@<project>.iam` (created by Pulumi in
-- postgres.ts). It performs the series_media cut-over (FindMediaByID + UPDATE)
-- after generating WebP variants, so it needs read+write on the app schema.
--
-- The media pipeline never had DB access before (media-processor never ran
-- successfully), so this user holds no privileges on existing or future objects.
-- This re-runs the same generic, idempotent grant loop used for the other IAM
-- users (fan-api, organizer-console-api): it covers BOTH existing objects
-- (GRANT ... ON ALL ... IN SCHEMA app) and future ones (ALTER DEFAULT PRIVILEGES),
-- and is a no-op for roles already granted.
--
-- Runs as the postgres (cloudsqlsuperuser) user via the Atlas Operator. The
-- media-consumer Cloud SQL IAM user MUST already exist (Pulumi `pulumi up` creates
-- it before this migration applies); the loop skips any IAM role not yet present.
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
