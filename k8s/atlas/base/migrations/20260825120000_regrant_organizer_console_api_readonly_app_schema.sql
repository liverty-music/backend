-- Re-grant READ-ONLY app-schema privileges to the `organizer-console-api` Cloud
-- SQL IAM service-account user (catch-up for a create-order gap).
--
-- WHY THIS EXISTS (in addition to 20260825000000):
-- The original grant migration (20260825000000) ran via the Atlas Operator
-- BEFORE Pulumi (`pulumi up`) created the `organizer-console-api@<project>.iam`
-- Cloud SQL user. Its role-name loop therefore matched nothing and granted
-- nothing, and Atlas will not re-run an already-applied migration. In prod this
-- surfaced as the organizer server failing every read with
--   ERROR: relation "organizers" does not exist (SQLSTATE 42P01)
-- because the role holds no USAGE on schema `app`, making its tables invisible
-- even though the DSN sets search_path=app.
--
-- The IAM user now exists (Pulumi applied), so this fresh migration re-runs the
-- same SELECT-only grant and takes effect. It is idempotent and still skips the
-- role if (for any environment) it is not yet present.
--
-- Read-only invariant reminder (see 20260825000000): any future generic
-- `%@%.iam` GRANT loop MUST exclude `organizer-console-api@%` to avoid
-- re-granting write access — grants are additive and there is no REVOKE.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA app TO %I', iam_role);
    EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA app TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT ON TABLES TO %I', iam_role);
  END LOOP;
END
$$;
