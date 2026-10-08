-- Restrict the app-schema privileges of the `organizer-console-api` and
-- `zitadel` Cloud SQL IAM service-account users (backend#558).
--
-- WHY: 20260901000000_grant_media_consumer_app_schema ran the generic
-- `rolname LIKE '%@%.iam'` grant loop without excluding read-only roles. It
-- granted SELECT, INSERT, UPDATE, DELETE on every app table and set the same
-- ALTER DEFAULT PRIVILEGES for every IAM service account. Migrations run as
-- postgres, so every table created since inherits full CRUD for those roles:
--
--   organizer-console-api — meant to be read-only plus a narrow write set; it
--     also serves the unauthenticated ReceptionService, and admissions /
--     rejected_scans were meant to be append-only by privilege
--     (20261008010000), yet UPDATE / DELETE went through.
--   zitadel — uses its own `zitadel` database and needs nothing in app.
--
-- WHAT: for organizer-console-api, revoke every write privilege (table and
-- default), keep SELECT and the SELECT default, then re-grant only the writes
-- the organizer server performs (traced from internal/di/provider.go through
-- the organizer handlers, usecases and rdb repositories):
--
--   series                  INSERT; UPDATE (title, type, source_url,
--                           description, visibility, unlisted_token,
--                           publish_state, published_at, cancelled_at)
--                           — Concert CreateDraft / UpdateDraft / Publish /
--                           Cancel / RegenerateToken
--   draft_events            INSERT, DELETE — CreateDraft / UpdateDraft / Publish
--   draft_series_performers INSERT, DELETE — CreateDraft / UpdateDraft / Publish
--   venues                  INSERT — draft venue get-or-create
--   events                  INSERT; UPDATE (series_id) — Publish inserts new
--                           events and claims discovered ones. The column
--                           privilege also covers the event row lock
--                           (SELECT ... FOR NO KEY UPDATE) taken by
--                           ReceptionLink Issue.
--   event_performers        INSERT — Publish
--   concerts                INSERT — Publish
--   staged_concerts         DELETE — Publish supersedes staged rows
--   media                   INSERT — Concert AttachMedia
--   lottery_sales_phases    INSERT; UPDATE (verification_requirement)
--                           — Lottery Configure / SetVerificationRequirement
--   organizer_connected_accounts
--                           INSERT; UPDATE (account_ref, status,
--                           status_synced_at) — PayoutOnboarding Get
--                           (upsert + status sync)
--   reception_links         INSERT; UPDATE (status, bound_public_key,
--                           bound_at, token, revoked_at) — ReceptionLink
--                           Issue / Revoke, Reception Open (bind, FOR UPDATE),
--                           Reception Admit (FOR SHARE)
--   tickets                 UPDATE (admitted_at) — Reception Admit
--   admissions              INSERT only — Reception Admit (append-only)
--   rejected_scans          INSERT only — Reception Admit (append-only)
--
-- The organizer server uses no sequences (all keys are application-generated
-- UUIDs), so every sequence privilege is revoked as well.
--
-- For zitadel: revoke everything on app tables and sequences, the default
-- privileges, and USAGE on schema app.
--
-- fan-api, admin-console-api, backend-app and media-consumer are unchanged.
--
-- ┌───────────────────────────────────────────────────────────────────────────┐
-- │ CONVENTION — GENERIC `%@%.iam` GRANT LOOPS MUST EXCLUDE NARROW ROLES       │
-- │                                                                           │
-- │ A generic catch-up loop over `rolname LIKE '%@%.iam'` that GRANTs          │
-- │ SELECT, INSERT, UPDATE, DELETE (or sets ALTER DEFAULT PRIVILEGES) reaches  │
-- │ every IAM service account. Any such loop MUST append                       │
-- │   AND rolname NOT LIKE 'organizer-console-api@%'                          │
-- │   AND rolname NOT LIKE 'zitadel@%'                                        │
-- │ (extend the list as more narrow-privilege workloads are added). Prefer     │
-- │ granting a new workload's role by its own name pattern instead.            │
-- │ rdb.TestMigrationGrants_IAMRoles (migration_grants_integration_test.go)    │
-- │ applies every migration with these roles present and fails if either role  │
-- │ gains a privilege outside the list above.                                  │
-- └───────────────────────────────────────────────────────────────────────────┘
--
-- Idempotent: REVOKE of a privilege not held and re-GRANT of a held one are
-- no-ops. Each loop skips a role that does not exist in the environment.
-- ALTER DEFAULT PRIVILEGES without FOR ROLE targets the defaults of the role
-- running this migration (postgres via the Atlas Operator), which is the role
-- that set them in the earlier grant migrations.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    -- Drop every write privilege, existing and default. Revoking a
    -- table-level privilege also revokes the column-level grants of the same
    -- kind, so the narrow grants below are re-applied from scratch.
    EXECUTE format('REVOKE INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON ALL TABLES IN SCHEMA app FROM %I', iam_role);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA app FROM %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app REVOKE INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLES FROM %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app REVOKE ALL ON SEQUENCES FROM %I', iam_role);

    -- Keep read access on existing and future tables.
    EXECUTE format('GRANT USAGE ON SCHEMA app TO %I', iam_role);
    EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA app TO %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT ON TABLES TO %I', iam_role);

    -- Concert authoring.
    EXECUTE format('GRANT INSERT ON app.series TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (title, type, source_url, description, visibility, unlisted_token, publish_state, published_at, cancelled_at) ON app.series TO %I', iam_role);
    EXECUTE format('GRANT INSERT, DELETE ON app.draft_events TO %I', iam_role);
    EXECUTE format('GRANT INSERT, DELETE ON app.draft_series_performers TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.venues TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.events TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (series_id) ON app.events TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.event_performers TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.concerts TO %I', iam_role);
    EXECUTE format('GRANT DELETE ON app.staged_concerts TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.media TO %I', iam_role);

    -- Lottery configuration.
    EXECUTE format('GRANT INSERT ON app.lottery_sales_phases TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (verification_requirement) ON app.lottery_sales_phases TO %I', iam_role);

    -- Payout onboarding.
    EXECUTE format('GRANT INSERT ON app.organizer_connected_accounts TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (account_ref, status, status_synced_at) ON app.organizer_connected_accounts TO %I', iam_role);

    -- Reception links and reception.
    EXECUTE format('GRANT INSERT ON app.reception_links TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (status, bound_public_key, bound_at, token, revoked_at) ON app.reception_links TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (admitted_at) ON app.tickets TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.admissions TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.rejected_scans TO %I', iam_role);
  END LOOP;

  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'zitadel@%.iam'
  LOOP
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA app FROM %I', iam_role);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA app FROM %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app REVOKE ALL ON TABLES FROM %I', iam_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA app REVOKE ALL ON SEQUENCES FROM %I', iam_role);
    EXECUTE format('REVOKE ALL ON SCHEMA app FROM %I', iam_role);
  END LOOP;
END
$$;
