-- Revoke every app-schema privilege from the retired `media-consumer` Cloud
-- SQL IAM service-account user.
--
-- The series image worker now runs as series-image-consumer
-- (20261012230000_grant_series_image_consumer_app_schema.sql), and the old
-- media-consumer user is about to be deleted from Cloud SQL. Postgres refuses
-- to drop a role that still holds privileges ("role cannot be dropped because
-- some objects depend on it"), so this removes everything
-- 20260901000000_grant_media_consumer_app_schema.sql (and any later
-- `%@%.iam` loop) gave it: privileges on existing tables and sequences, the
-- default privileges for future tables and sequences, and USAGE on schema app.
--
-- ALTER DEFAULT PRIVILEGES without FOR ROLE targets the defaults of the role
-- running this migration (postgres via the Atlas Operator). Every grant
-- migration ran as that role without FOR ROLE, so it is the only grantor of
-- the media-consumer default privileges.
--
-- Idempotent: REVOKE of a privilege not held is a no-op, and the loop skips
-- the role when it does not exist (already deleted, or never created).
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN SELECT rolname FROM pg_roles WHERE rolname LIKE 'media-consumer@%.iam' LOOP
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA app FROM %I', iam_role);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA app FROM %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app REVOKE ALL ON TABLES FROM %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app REVOKE ALL ON SEQUENCES FROM %I', iam_role);
    EXECUTE format('REVOKE ALL ON SCHEMA app FROM %I', iam_role);
  END LOOP;
END
$$;
