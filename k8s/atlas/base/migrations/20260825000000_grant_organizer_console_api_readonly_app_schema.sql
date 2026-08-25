-- Grant READ-ONLY app-schema privileges to the `organizer-console-api` Cloud SQL
-- IAM service-account user.
--
-- The organizer-console-api workload serves a read-only Connect surface
-- (OrganizerService.Get + ListArtists). It authenticates to Cloud SQL as its own
-- IAM DB user (`organizer-console-api@<project>.iam`, created by Pulumi in
-- postgres.ts) and MUST hold SELECT only — no INSERT/UPDATE/DELETE and no
-- sequence privileges — on the app schema.
--
-- This deliberately does NOT reuse the generic `%@%.iam` read-WRITE grant loop
-- used by the fan-api / admin-console-api migrations (that would over-grant write
-- access to a read-only surface). It targets the organizer role by name pattern
-- (project-agnostic: dev + prod) and grants SELECT on existing AND future tables
-- via ALTER DEFAULT PRIVILEGES.
--
-- ┌───────────────────────────────────────────────────────────────────────────┐
-- │ CONVENTION — KEEP organizer-console-api READ-ONLY                          │
-- │                                                                           │
-- │ Postgres grants are additive and there is NO REVOKE anywhere in this       │
-- │ migrations directory. The repo's established "catch-up" pattern for        │
-- │ onboarding a new workload's IAM user re-runs a generic                     │
-- │   FOR iam_role IN SELECT rolname FROM pg_roles WHERE rolname LIKE '%@%.iam' │
-- │ loop that GRANTs SELECT,INSERT,UPDATE,DELETE to EVERY matching role         │
-- │ (see 20260816000000_grant_admin_console_api… and                          │
-- │ 20260817010000_grant_fan_api…). `organizer-console-api@<project>.iam`       │
-- │ matches `%@%.iam`, so any FUTURE generic-loop grant migration would         │
-- │ silently re-grant it full read-WRITE, defeating this file's invariant.      │
-- │                                                                           │
-- │ Therefore: any future generic `%@%.iam` GRANT loop MUST exclude read-only   │
-- │ roles, e.g. append                                                        │
-- │   AND rolname NOT LIKE 'organizer-console-api@%'                            │
-- │ (extend the exclusion list as more read-only workloads are added).         │
-- └───────────────────────────────────────────────────────────────────────────┘
--
-- Runs as the postgres (cloudsqlsuperuser) user via the Atlas Operator. The
-- organizer-console-api Cloud SQL IAM user must already exist (Pulumi `pulumi up`
-- creates it before this migration applies); the loop skips the role if absent,
-- so the grant is a no-op until the user exists and idempotent thereafter.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  -- Cloud SQL registers IAM service-account users as `<name>@<project>.iam`.
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA app TO %I', iam_role);
    EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA app TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT ON TABLES TO %I', iam_role);
  END LOOP;
END
$$;
